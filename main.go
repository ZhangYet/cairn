package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/pflag"
)

const version = "0.2.1"

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "Error: "+format+"\n", args...)
	os.Exit(1)
}

func main() {
	cfgPath := "~/.cairn.toml"

	if len(os.Args) >= 2 && os.Args[1] == "-h" || len(os.Args) >= 2 && os.Args[1] == "--help" {
		printHelp("")
		return
	}

	i := 1
	for i < len(os.Args) {
		if os.Args[i] == "-c" || os.Args[i] == "--config" {
			if i+1 < len(os.Args) {
				cfgPath = os.Args[i+1]
				os.Args = append(os.Args[:i], os.Args[i+2:]...)
				continue
			}
		}
		i++
	}

	config, err := loadConfig(cfgPath)
	if err != nil {
		fatal("%v", err)
	}

	if len(os.Args) < 2 {
		printHelp("")
		fatal("no command specified")
	}

	cmd := os.Args[1]
	os.Args = append([]string{os.Args[0]}, os.Args[2:]...)

	switch cmd {
	case "help":
		sub := ""
		if len(os.Args) > 1 {
			sub = os.Args[1]
		}
		printHelp(sub)
	case "post":
		if len(os.Args) < 2 {
			printHelp("post")
			fatal("post requires a subcommand: send or edit")
		}
		sub := os.Args[1]
		os.Args = append([]string{os.Args[0]}, os.Args[2:]...)
		switch sub {
		case "send":
			cmdPostSend(config)
		case "edit":
			cmdPostEdit(config)
		default:
			printHelp("post")
			fatal("unknown post subcommand: %s", sub)
		}
	case "write":
		cmdWrite(config)
	case "dict":
		cmdDict()
	case "geo":
		if len(os.Args) < 2 {
			printHelp("geo")
			fatal("geo requires a subcommand: list or route")
		}
		sub := os.Args[1]
		os.Args = append([]string{os.Args[0]}, os.Args[2:]...)
		switch sub {
		case "list":
			cmdGeoList(config)
		case "route":
			cmdGeoRoute(config)
		default:
			printHelp("geo")
			fatal("unknown geo subcommand: %s", sub)
		}
	case "bird":
		if len(os.Args) < 2 {
			printHelp("bird")
			fatal("bird requires a subcommand: download, list, play, or quiz")
		}
		sub := os.Args[1]
		os.Args = append([]string{os.Args[0]}, os.Args[2:]...)
		switch sub {
		case "download":
			cmdBirdDownload(config)
		case "list":
			cmdBirdList()
		case "play":
			cmdBirdPlay()
		case "quiz":
			cmdBirdQuiz()
		default:
			printHelp("bird")
			fatal("unknown bird subcommand: %s", sub)
		}
	case "fitbit":
		if len(os.Args) < 2 {
			printHelp("fitbit")
			fatal("fitbit requires a subcommand: morning or dump")
		}
		sub := os.Args[1]
		os.Args = append([]string{os.Args[0]}, os.Args[2:]...)
		switch sub {
		case "morning":
			cmdFitbitMorning(config)
		case "dump":
			cmdFitbitDump(config)
		default:
			printHelp("fitbit")
			fatal("unknown fitbit subcommand: %s", sub)
		}
	default:
		printHelp("")
		fatal("unknown command: %s", cmd)
	}
}

// ----- post -----

func cmdPostSend(config *Config) {
	fs := pflag.NewFlagSet("post send", pflag.ExitOnError)
	content := fs.StringP("post", "p", "", "Content to post")
	file := fs.StringP("file", "f", "", "Read content from a file")
	photoStr := fs.StringP("photo", "P", "", "Comma-separated photo paths")
	fs.Parse(os.Args)

	var photos []string
	if *photoStr != "" {
		for _, p := range strings.Split(*photoStr, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				photos = append(photos, p)
			}
		}
		for _, p := range fs.Args() {
			p = strings.TrimSpace(p)
			if p != "" {
				photos = append(photos, p)
			}
		}
	}

	if len(photos) == 0 && *content == "" && *file == "" {
		printHelp("post send")
		fatal("requires -p, -f, or -P")
	}

	var text string
	var err error
	if *file != "" {
		text, err = readFileContent(*file)
		if err != nil {
			fatal("%v", err)
		}
	} else {
		text = *content
	}

	if err := requireTelegram(config); err != nil {
		fatal("%v", err)
	}

	if len(photos) == 1 {
		if _, err := postPhotoToTelegram(config.Telegram.BotToken, config.Telegram.ChannelID, photos[0], text); err != nil {
			fatal("%v", err)
		}
	} else if len(photos) > 1 {
		if _, err := postMultiplePhotosToTelegram(config.Telegram.BotToken, config.Telegram.ChannelID, photos, text); err != nil {
			fatal("%v", err)
		}
	} else {
		if _, err := postToTelegram(config.Telegram.BotToken, config.Telegram.ChannelID, text); err != nil {
			fatal("%v", err)
		}
	}
	fmt.Fprintln(os.Stderr, "Posted.")
}

func cmdPostEdit(config *Config) {
	if err := requireTelegram(config); err != nil {
		fatal("%v", err)
	}

	fs := pflag.NewFlagSet("post edit", pflag.ExitOnError)
	content := fs.StringP("post", "p", "", "New content")
	file := fs.StringP("file", "f", "", "Read new content from a file")
	photoStr := fs.StringP("photo", "P", "", "New photo path (single file)")
	fs.Parse(os.Args)

	if fs.NArg() < 1 {
		printHelp("post edit")
		fatal("requires message ID as argument")
	}
	msgID, err := strconv.ParseInt(fs.Arg(0), 10, 64)
	if err != nil || msgID <= 0 {
		fatal("message ID must be a positive integer")
	}

	if *photoStr != "" {
		caption := *content
		if *file != "" {
			caption, err = readFileContent(*file)
			if err != nil {
				fatal("%v", err)
			}
		}
		if err := editMessageMediaTelegram(config.Telegram.BotToken, config.Telegram.ChannelID, msgID, *photoStr, caption); err != nil {
			fatal("%v", err)
		}
		return
	}

	if *content == "" && *file == "" {
		fatal("requires -p or -f for new content (or -P to replace image)")
	}
	if *content != "" && *file != "" {
		fatal("cannot use both -p and -f")
	}

	var newContent string
	if *file != "" {
		newContent, err = readFileContent(*file)
		if err != nil {
			fatal("%v", err)
		}
	} else {
		newContent = *content
	}

	err = editMessageTelegram(config.Telegram.BotToken, config.Telegram.ChannelID, msgID, newContent)
	if err != nil && (strings.Contains(err.Error(), "message has no text") || strings.Contains(err.Error(), "no text in the message to edit")) {
		err = editMessageCaptionTelegram(config.Telegram.BotToken, config.Telegram.ChannelID, msgID, newContent)
	}
	if err != nil {
		fatal("%v", err)
	}
	fmt.Fprintln(os.Stderr, "Edited.")
}

// ----- write -----

func cmdWrite(config *Config) {
	fs := pflag.NewFlagSet("write", pflag.ExitOnError)
	promptFile := fs.StringP("file", "f", "", "Prompt file path")
	outputFile := fs.StringP("output", "o", "", "Output file path")
	fs.Parse(os.Args)

	if *promptFile == "" {
		printHelp("write")
		fatal("requires -f/--file")
	}
	if err := Writer(config, *promptFile, *outputFile); err != nil {
		fatal("%v", err)
	}
}

// ----- dict -----

func cmdDict() {
	if len(os.Args) < 2 {
		printHelp("dict")
		fatal("requires a word (e.g. cairn dict hello)")
	}
	if err := Dict(strings.Join(os.Args[1:], " ")); err != nil {
		fatal("%v", err)
	}
}

// ----- geo -----

func cmdGeoList(config *Config) {
	fs := pflag.NewFlagSet("geo list", pflag.ExitOnError)
	placesFile := fs.StringP("file", "f", "", "Places file (one per line, - for stdin)")
	fs.Parse(os.Args)

	if *placesFile == "" {
		printHelp("geo list")
		fatal("requires -f/--file")
	}
	places, err := readPlacesFromFile(*placesFile)
	if err != nil {
		fatal("%v", err)
	}
	if len(places) == 0 {
		fatal("no place names in file")
	}
	if err := GeocodePlaces(config, places); err != nil {
		fatal("%v", err)
	}
}

func cmdGeoRoute(config *Config) {
	fs := pflag.NewFlagSet("geo route", pflag.ExitOnError)
	placesFile := fs.StringP("file", "f", "", "Places file (one per line, first = start)")
	open := fs.Bool("open", false, "Open path (don't return to start)")
	fs.Parse(os.Args)

	if *placesFile == "" {
		printHelp("geo route")
		fatal("requires -f/--file")
	}
	places, err := readPlacesFromFile(*placesFile)
	if err != nil {
		fatal("%v", err)
	}
	if len(places) == 0 {
		fatal("no place names in file")
	}
	if err := TravelPlacesRoute(config, places, !*open); err != nil {
		fatal("%v", err)
	}
}

// ----- bird -----

func cmdBirdDownload(config *Config) {
	fs := pflag.NewFlagSet("bird download", pflag.ExitOnError)
	audioDir := fs.String("audio-dir", "", "Audio storage directory (default ~/.cairn_bird_audio/)")
	fs.Parse(os.Args)

	name := strings.Join(fs.Args()[1:], " ")
	if name == "" {
		printHelp("bird download")
		fatal("requires a bird name")
	}
	if err := BirdDownload(name, *audioDir, config.Ebird.XCAPIKey); err != nil {
		fatal("%v", err)
	}
}

func cmdBirdList() {
	if err := BirdList(); err != nil {
		fatal("%v", err)
	}
}

func cmdBirdPlay() {
	if len(os.Args) < 2 {
		printHelp("bird play")
		fatal("requires a bird name")
	}
	name := strings.Join(os.Args[1:], " ")
	if err := BirdPlay(name); err != nil {
		fatal("%v", err)
	}
}

func cmdBirdQuiz() {
	fs := pflag.NewFlagSet("bird quiz", pflag.ExitOnError)
	audioDir := fs.String("audio-dir", "", "Audio storage directory (default ~/.cairn_bird_audio/)")
	fs.Parse(os.Args)

	n := 4
	args := fs.Args()[1:]
	if len(args) > 0 {
		if v, err := strconv.Atoi(args[0]); err == nil && v >= 2 {
			n = v
		}
	}
	if err := BirdQuiz(n, *audioDir); err != nil {
		fatal("%v", err)
	}
}

// ----- fitbit -----

func cmdFitbitMorning(config *Config) {
	if err := requireTelegram(config); err != nil {
		fatal("%v", err)
	}
	fs := pflag.NewFlagSet("fitbit morning", pflag.ExitOnError)
	text := fs.StringP("post", "p", "", "Additional text")
	file := fs.StringP("file", "f", "", "Additional text from file")
	fs.Parse(os.Args)

	var additional string
	var err error
	if *file != "" {
		additional, err = readFileContent(*file)
		if err != nil {
			fatal("%v", err)
		}
	} else {
		additional = *text
	}
	if err := Morning(config, additional); err != nil {
		fatal("%v", err)
	}
}

func cmdFitbitDump(config *Config) {
	if len(os.Args) < 2 {
		printHelp("fitbit dump")
		fatal("requires output file path")
	}
	if err := DumpSleepJSON(config, os.Args[1]); err != nil {
		fatal("%v", err)
	}
}

// ----- help -----

func printHelp(command string) {
	if cmd, _, ok := strings.Cut(command, " "); ok {
		command = cmd
	}
	switch command {
	case "post":
		fmt.Printf(`cairn post — Send messages to Telegram

Usage:
  cairn post send  -p <text>                 Post text
  cairn post send  -f <file>                 Post file content
  cairn post send  -P <img> -p <caption>     Post photo with caption
  cairn post send  -P a.jpg,b.jpg -p <text>  Post multiple photos
  cairn post edit  <id> -p <text>            Edit message text
  cairn post edit  <id> -P <img> -p <cap>    Replace photo + caption
`)
	case "write":
		fmt.Printf(`cairn write — Generate text with LLM (OpenAI / OpenRouter)

Usage:
  cairn write -f <prompt_file>               Stream output
  cairn write -f <prompt_file> -o <out>      Save to file
`)
	case "dict":
		fmt.Printf(`cairn dict — Look up word in dictionary

Usage:
  cairn dict <word>
`)
	case "geo":
		fmt.Printf(`cairn geo — Geocode places and optimize routes

Usage:
  cairn geo list  -f <file>                 Geocode places (one per line)
  cairn geo route -f <file>                 Round-trip optimization
  cairn geo route -f <file> --open          One-way optimization
`)
	case "bird":
		fmt.Printf(`cairn bird — Bird sound identification

Usage:
  cairn bird download <name>                 Download bird (CN/EN/sci name)
  cairn bird list                            List downloaded birds
  cairn bird play <name>                     Play recordings
  cairn bird quiz [N]                        Start quiz (default 4 choices)
        --audio-dir DIR                     Audio storage (default ~/.cairn_bird_audio/)
`)
	case "fitbit":
		fmt.Printf(`cairn fitbit — Fitbit sleep data

Usage:
  cairn fitbit morning [text]                Post sleep report + optional text
  cairn fitbit morning -f <file>             Post sleep report + file content
  cairn fitbit dump <path>                   Export raw sleep JSON
`)
	default:
		fmt.Printf(`cairn — CLI toolkit
Version: %s

Usage:
  cairn [-c <config>] <command> [args...]

Commands:
  post        Telegram messaging
  write       LLM writing (OpenAI / OpenRouter)
  dict        Dictionary lookup
  geo         Geocoding and route optimization
  bird        Bird sound quiz (eBird + Xeno-Canto)
  fitbit      Fitbit sleep data

Global:
  -c, --config PATH   Config file (default: ~/.cairn.toml)

Run 'cairn help <command>' for details.
`, version)
	}
}
