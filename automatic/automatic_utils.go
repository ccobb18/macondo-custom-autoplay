package automatic

// Data collection for automatic game. Allow computer vs computer games, etc.

import (
	"bufio"
	"context"
	"errors"
	"expvar"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/samber/lo"
	"golang.org/x/sync/errgroup"

	"github.com/domino14/macondo/ai/bot"
	"github.com/domino14/macondo/config"
	pb "github.com/domino14/macondo/gen/api/proto/macondo"
)

var (
	CVCCounter *expvar.Int
	IsPlaying  *expvar.Int
)

func init() {
	CVCCounter = expvar.NewInt("cvcCounter")
	IsPlaying = expvar.NewInt("isPlaying")
}

// CompVsCompStatic plays out a game to the end using best static turns.
func (r *GameRunner) CompVsCompStatic(addToHistory bool) error {
	err := r.Init(
		[]AutomaticRunnerPlayer{
			{"", "", pb.BotRequest_HASTY_BOT, 0, false},
			{"", "", pb.BotRequest_HASTY_BOT, 0, false},
		})

	if err != nil {
		return err
	}
	err = r.playFull(addToHistory, 0)
	if err != nil {
		return err
	}
	log.Debug().Msgf("Game over. Score: %v - %v", r.game.PointsFor(0),
		r.game.PointsFor(1))
	return nil
}

func (r *GameRunner) playFull(addToHistory bool, gidx int) error {
	r.StartGame(gidx)
	log.Trace().Msgf("playing full, game %v", r.game.History().Uid)

	for r.game.Playing() == pb.PlayState_PLAYING {
		err := r.PlayBestTurn(r.game.PlayerOnTurn(), addToHistory)
		if err != nil {
			return err
		}
	}

	if r.gamechan != nil {
		r.gamechan <- fmt.Sprintf("%v,%d,%d,%d,%d,%d,%d,%s\n",
			r.game.Uid(),
			r.game.PointsForNick("p1"),
			r.game.PointsForNick("p2"),
			r.game.BingosForNick("p1"),
			r.game.BingosForNick("p2"),
			r.game.TurnsForNick("p1"),
			r.game.TurnsForNick("p2"),
			r.game.FirstPlayer().RealName,
		)
	}
	return nil
}

func prettyName(b pb.BotRequest_BotCode) string {
	protoName := b.String()

	components := strings.Split(protoName, "_")
	return strings.Join(lo.Map(components, func(i string, idx int) string {
		return strings.Title(strings.ToLower(i))
	}), "")
}

func playerNames(players []AutomaticRunnerPlayer) []string {
	botct := map[string]int{}
	botctorig := map[string]int{}
	for _, p := range players {
		s := p.BotCode.String()
		botct[s]++
		botctorig[s]++
	}
	names := []string{}
	for _, p := range players {
		s := p.BotCode.String()

		if botct[s] == botctorig[s] {
			names = append(names, prettyName(p.BotCode))
		} else {
			names = append(names, prettyName(p.BotCode)+strconv.Itoa(botctorig[s]-botct[s]))
		}
		botct[s]--
	}
	return names
}

type Job struct{ gidx int }

func StartCompVCompStaticGames(ctx context.Context, cfg *config.Config,
	numGames int, block bool, threads int, sleep int,
	outputFilename, plFilename, utFilename, autFilename,
	lvafFilename, lexicon, letterDistribution string,
	players []AutomaticRunnerPlayer) error {

	playabilityValues := make(map[string]int)
	utilityValues := make(map[string]float64)
	alphagramUtilityValues := make(map[string]float64)
	leaveFreqs := make(map[string]int)
	leaveValues := make(map[string]float64)

	// fill in the playability and utility values from existing files so
	// we can pick up where we left off.
	// this will overwrite the file at the end with the new values and create
	// a .bak file with the old values in case of issues.
	if plFilename != "" {
		err := loadKeyValueFile(
			plFilename,
			strconv.Atoi,
			playabilityValues,
		)
		if err != nil {
			return err
		}
	}
	if utFilename != "" {
		err := loadKeyValueFile(
			utFilename,
			func(s string) (float64, error) {
				return strconv.ParseFloat(s, 64)
			},
			utilityValues,
		)
		if err != nil {
			return err
		}
	}
	if autFilename != "" {
		err := loadKeyValueFile(
			autFilename,
			func(s string) (float64, error) {
				return strconv.ParseFloat(s, 64)
			},
			alphagramUtilityValues,
		)
		if err != nil {
			return err
		}
	}
	if lvafFilename != "" {
		err := loadLeaveValuesAndFreqsFile(
			lvafFilename,
			leaveFreqs,
			leaveValues,
		)
		if err != nil {
			return err
		}
	}

	if len(players) != 2 {
		return errors.New("must have two players")
	}

	if threads > 1 && lo.SomeBy(players, func(p AutomaticRunnerPlayer) bool {
		return bot.HasEndgame(p.BotCode) || bot.HasPreendgame(p.BotCode)
	}) {
		return errors.New("cannot run multiple games in parallel if either player uses endgame or pre-endgame")
	}

	if IsPlaying.Value() > 0 {
		return errors.New("games are already being played, please wait till complete")
	}

	logfile, err := os.Create(outputFilename)
	if err != nil {
		return err
	}

	glfilename := filepath.Join(
		path.Dir(outputFilename),
		"games-"+path.Base(outputFilename))
	gamelogfile, err := os.Create(glfilename)
	if err != nil {
		return err
	}

	log.Info().Msgf("Starting %v games, %v threads", numGames, threads)

	CVCCounter.Set(0)
	jobs := make(chan Job, threads*5)
	logChan := make(chan string, 100)
	gameChan := make(chan string, 10)

	var wg sync.WaitGroup
	// var fwg sync.WaitGroup

	g, ctx := errgroup.WithContext(ctx)
	addToHistory := false
	if lo.SomeBy(players, func(p AutomaticRunnerPlayer) bool {
		return bot.HasInfer(p.BotCode)
	}) {
		addToHistory = true
	}

	for i := 1; i <= threads; i++ {
		wg.Add(1)
		i := i
		g.Go(func() error {
			defer wg.Done()
			r := GameRunner{logchan: logChan, gamechan: gameChan,
				config: cfg, lexicon: lexicon, letterDistribution: letterDistribution}
			r.PlayabilityValues = playabilityValues
			r.UtilityValues = utilityValues
			r.AlphagramUtilityValues = alphagramUtilityValues
			r.LeaveFreqs = leaveFreqs
			r.LeaveValues = leaveValues
			err := r.Init(players)
			if err != nil {
				log.Err(err).Msg("error initializing runner")
				return err
			}

			IsPlaying.Add(1)
			defer IsPlaying.Add(-1)
			for j := range jobs {
				err = r.playFull(addToHistory, j.gidx)
				if err != nil {
					log.Err(err).Int("job", j.gidx).Msg("error-playFull")
					return err
				}
				CVCCounter.Add(1)
			}
			log.Err(err).Msgf("exiting-gameplay-thread-%d", i)
			return nil
		})
	}

	g.Go(func() error {
		queuingJobs := true
		i := 0
	gameLoop:
		for queuingJobs {
			select {
			case jobs <- Job{i}:
				if i%250 == 0 && i > 0 {
					log.Info().Msgf("Queued %v jobs", i)

					if sleep > 0 {
						log.Info().Msgf("Taking a rest -___-")
						time.Sleep(time.Duration(sleep) * time.Second)
						log.Info().Msgf("Done resting :)")
					}
				}
				i++
			case <-ctx.Done():
				// exit early
				log.Err(ctx.Err()).Msg("Context done")
				log.Info().Msg("Got stop signal, exiting soon...")
				break gameLoop
			default:
				// do nothing

			}
			if i == numGames {
				queuingJobs = false
			}
		}

		close(jobs)
		log.Info().Int("jobsQueued", i).Msg("Finished queueing all jobs.")
		wg.Wait()
		log.Info().Msg("All games finished.")
		close(logChan)
		close(gameChan)
		log.Info().Msg("Exiting feeder subroutine!")
		return ctx.Err()
	})

	g.Go(func() error {
		logfile.WriteString("playerID,gameID,turn,rack,play,score,totalscore,tilesplayed,leave,equity,tilesremaining,oppscore\n")
		for msg := range logChan {
			logfile.WriteString(msg)
		}
		logfile.Close()

		type plkv struct {
			Key   string
			Value int
		}
		var plss []plkv
		for k, v := range playabilityValues {
			plss = append(plss, plkv{k, v})
		}
		sort.Slice(plss, func(i, j int) bool {
			return plss[i].Value > plss[j].Value
		})
		err = replaceFileSafely(plFilename, func(f *os.File) error {
			for _, kv := range plss {
				valueAsStr := strconv.Itoa(kv.Value)
				f.WriteString(kv.Key + "," + valueAsStr + "\n")
			}
			return nil
		})
		if err != nil {
			return err
		}

		type utkv struct {
			Key   string
			Value float64
		}
		var utss []utkv
		for k, v := range utilityValues {
			utss = append(utss, utkv{k, v})
		}
		sort.Slice(utss, func(i, j int) bool {
			return utss[i].Value > utss[j].Value
		})
		err = replaceFileSafely(utFilename, func(f *os.File) error {
			for _, kv := range utss {
				valueAsStr := strconv.FormatFloat(kv.Value, 'f', 2, 64)
				f.WriteString(kv.Key + "," + valueAsStr + "\n")
			}
			return nil
		})
		if err != nil {
			return err
		}

		var autss []utkv
		for k, v := range alphagramUtilityValues {
			autss = append(autss, utkv{k, v})
		}
		sort.Slice(autss, func(i, j int) bool {
			return autss[i].Value > autss[j].Value
		})
		err = replaceFileSafely(autFilename, func(f *os.File) error {
			for _, kv := range autss {
				valueAsStr := strconv.FormatFloat(kv.Value, 'f', 2, 64)
				f.WriteString(kv.Key + "," + valueAsStr + "\n")
			}
			return nil
		})
		if err != nil {
			return err
		}

		type lvafRow struct {
			Leave string
			Freq  int
			Value float64
		}
		var rows []lvafRow
		for leave, freq := range leaveFreqs {
			rows = append(rows, lvafRow{
				Leave: leave,
				Freq:  freq,
				Value: leaveValues[leave],
			})
		}
		sort.Slice(rows, func(i, j int) bool {
			return rows[i].Freq > rows[j].Freq
		})
		if lvafFilename != "" {
			err := replaceFileSafely(
				lvafFilename,
				func(f *os.File) error {
					for _, row := range rows {
						_, err := fmt.Fprintf(
							f,
							"%s,%d,%.1f\n",
							row.Leave,
							row.Freq,
							row.Value,
						)
						if err != nil {
							return err
						}
					}
					return nil
				},
			)
			if err != nil {
				return err
			}
		}

		log.Info().Msg("Exiting turn logger goroutine!")
		return nil
	})

	g.Go(func() error {
		pnames := playerNames(players)
		header := fmt.Sprintf("gameID,%s_score,%s_score,%s_bingos,%s_bingos,%s_turns,%s_turns,first\n",
			pnames[0], pnames[1], pnames[0], pnames[1], pnames[0], pnames[1])

		gamelogfile.WriteString(header)
		for msg := range gameChan {
			gamelogfile.WriteString(msg)
		}
		gamelogfile.Close()
		log.Info().Msg("Exiting game logger goroutine!")
		return nil
	})

	if block {
		err = g.Wait()
		return err
	}
	return nil

}

func loadKeyValueFile[T any](
	filename string,
	parse func(string) (T, error),
	dest map[string]T,
) error {
	log.Info().Msgf("Reading existing values from: %q", filename)

	f, err := os.Open(filename)
	if err != nil {
		if os.IsNotExist(err) {
			log.Info().Msgf("File does not exist, starting fresh: %q", filename)
			return nil
		}
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		parts := strings.Split(line, ",")
		if len(parts) != 2 {
			log.Info().Msgf("skipping malformed line: %q", line)
			continue
		}

		value, err := parse(parts[1])
		if err != nil {
			log.Info().Msgf("skipping line (bad number): %q", line)
			continue
		}

		dest[parts[0]] = value
	}

	return scanner.Err()
}

func backupFile(filename string) error {
	if filename == "" {
		return nil
	}

	_, err := os.Stat(filename)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}

	bak := filename + ".bak"

	src, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.Create(bak)
	if err != nil {
		return err
	}
	defer dst.Close()

	_, err = io.Copy(dst, src)
	return err
}

func loadLeaveValuesAndFreqsFile(
	filename string,
	leaveFreqs map[string]int,
	leaveValues map[string]float64,
) error {
	log.Info().Msgf("Reading existing leave values from: %q", filename)

	f, err := os.Open(filename)
	if err != nil {
		if os.IsNotExist(err) {
			log.Info().Msgf("File does not exist, starting fresh: %q", filename)
			return nil
		}
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		parts := strings.Split(line, ",")
		if len(parts) != 3 {
			log.Info().Msgf("skipping malformed line: %q", line)
			continue
		}

		freq, err := strconv.Atoi(parts[1])
		if err != nil {
			log.Info().Msgf("skipping line (bad frequency): %q", line)
			continue
		}

		value, err := strconv.ParseFloat(parts[2], 64)
		if err != nil {
			log.Info().Msgf("skipping line (bad value): %q", line)
			continue
		}

		leaveFreqs[parts[0]] = freq
		leaveValues[parts[0]] = value
	}

	return scanner.Err()
}

func replaceFileSafely(
	filename string,
	writeFunc func(*os.File) error,
) error {
	tmp := filename + ".tmp"

	f, err := os.Create(tmp)
	if err != nil {
		return err
	}

	if err := writeFunc(f); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}

	if err := f.Close(); err != nil {
		return err
	}

	if err := backupFile(filename); err != nil {
		return err
	}

	return os.Rename(tmp, filename)
}
