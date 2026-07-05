# macondo-custom-autoplay

A fork of macondo that enhances the autoplay feature to be able to calculate playability and utility values. "Playability" here means simply how many times a word was played across a sample of autoplayed games (either as a main word or a cross word). "Utility" corresponds to a slightly more useful metric which is also sometimes called "playability", e.g. [here](https://crosstables.livejournal.com/24367.html); it instead calculates roughly what the total equity loss would be across the sample of games from not knowing a given word.

To get playability and utility values, you can run something like

```
autoplay -botcode1 HASTY_BOT_2 -botcode2 HASTY_BOT_2 -pllogfile "/path/to/my/file/pllogtest.txt" -utlogfile "/path/to/my/file/utlogtest.txt" -autlogfile "/path/to/my/file/autlogtest.txt" -threads 1
```

Note: HASTY_BOT_2 is a version of HASTY_BOT that I created as a workaround; see the comment in ai/bot/filters.go.
Note: It currently breaks if you set threads to anything higher than 1; I should probably fix this.

I also added a `-sleep` option that will pause the autoplay for the specified number of seconds every so often so my poor old computer doesn't overheat :P

Potential future improvements:
* ~~Optional argument to pass in paths to existing playability/utility logfiles and have the calculations add to those existing values instead of starting from scratch~~ ~~(Done; pass filepaths into the `-plrlogfile` and `-utrlogfile` options (the "r" stands for "read"), and it will start from the values in those files and add to them.)~~ (EDIT 2026-07-05: changed the way this works for simplicity; now `-plrlogfile` etc. don't exist anymore. instead we will read from the `-pllogfile` etc. if they exist to get initial values, make a backup copy, and overwrite the files the end.)
* ~~A version of utility based on alphagrams rather than specific words~~ (Done; values printed to `-autlogfile`.)
* A version of alphagram utility that takes into account some baseline probability of being able to find a word. (That is, rather than calculating the equity loss based on not knowing any solution of the alphagram at all, calculate an expected equity loss given, say, a 30% chance of finding any given solution. This would be a naive/simple way to simulate the fact that in reality, sometimes you can find a solution to an alphagram despite not having studied it before.)
* Make it so that it's not doing the playability and utility calculations unless you actually pass in arguments for those options
* An option to have all words included in the playability/utility logfiles, even ones with playability/utility of 0
* An option to not print to the logfile (even the temp one), since it can get quite large if you run autoplay long enough
* Add documentation of the new options to `help autoplay`

Original readme below:

---

# macondo

A crossword board game solver. It may be the best one in the world (so far).

Current master build status:

![Build status](https://github.com/domino14/macondo/actions/workflows/build-and-deploy-bot.yml/badge.svg)

# What is a crossword board game?

A crossword board game is a board game where you take turns creating crosswords
with one or more players. Some examples are:

- Scrabble™️ Brand Crossword Game
- Words with Friends
- Lexulous
- Yahoo! Literati (defunct)

# How to use Macondo:

See the manual and information here:

https://domino14.github.io/macondo

# protoc

To generate pb files, run this in the macondo directory:

`go generate`

Make sure you have done

`go install google.golang.org/protobuf/cmd/protoc-gen-go@latest`

# Creating a new release

(Notes mostly for myself)

Tag the release; i.e. `git tag vX.Y.Z`, then `git push --tags`. This will kick off a github action that builds and uploads the latest binaries. Then you should generate some release notes manually.

# Using Triton

If you want to use the neural network model, I recommend you use Triton server
rather than the default Go server. It's _much_ faster, but it's not trivial to run locally.

If your macondo directory is at `$HOME/code/macondo` you would run `docker run` with these parameters:

```
docker run --gpus all --rm -p 8000:8000 -p 8001:8001 -p 8002:8002     -v $HOME/code/macondo/data/strategy/default/models/:/models     nvcr.io/nvidia/tritonserver:25.06-py3     tritonserver --model-repository=/models
```

You may need to install the NVIDIA Container Toolkit to use your GPU for inference.

Then you can run Macondo with the `MACONDO_TRITON_USE_TRITON` environment variable set to `true`.

### Attributions

Wolges-awsm is Copyright (C) 2020-2022 Andy Kurnia and released under the MIT license. It can be found at https://github.com/andy-k/wolges-awsm/. Macondo interfaces with it as a server.

KLV and KWG are Andy Kurnia's leave and word graph formats. They are small and fast! See more info at https://github.com/andy-k/wolges

Some of the code for the endgame solver was influenced by the MIT-licensed Chess solver Blunder. See code at https://github.com/algerbrex/blunder
