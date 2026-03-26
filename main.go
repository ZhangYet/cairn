package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/pflag"
)

const version = "0.2.1"

func printHelp() {
	fmt.Printf(`cairn - A command-line tool to post content to Telegram channels
Version: %s

Usage:
  cairn [flags]

Flags:
  -h, --help          Show this help message
  -c, --config PATH   Path to config file (default: ~/.cairn.toml)
  -p, --post TEXT     Content to post (can include tags with #)
  -f, --file PATH     Read content from a file
  -P, --photo PATH   Path to photo file(s) to post (comma or space-separated, caption from -p or -f)
  -m, --morning       Get Fitbit sleep data and post to Telegram channel
  -W, --writer PATH   Read setting from file, send to OpenAI or OpenRouter (streaming), get generated content
  -o, --output PATH   Write generated content to file (use with -W)
  -d, --dict WORD     Look up word meaning (Free Dictionary API)
  -F, --places-file PATH  Geocode places from file, one per line ([google] api_key); use - for stdin
  -T, --travel        With -F: optimize visit order (great-circle km); first line = start
      --travel-open   With -T: mode 2 — end at last stop; do not return to the first place (default: mode 1, round trip)
  -u, --update ID     Update message/caption by ID (-p/-f), or replace photo (-P with one file)

Examples:
  cairn -p "Hello world #tag1 #tag2"
  cairn -f message.txt
  cairn -P image.jpg -p "Photo caption #tag1"
  cairn -P image1.jpg,image2.jpg -p "Multiple photos"
  cairn -P image1.jpg image2.jpg -p "Multiple photos"
  cairn --photo image.jpg -f caption.txt
  cairn -c ~/.custom_cairn.toml -p "Custom config"
  cairn --morning
  cairn -W prompt.txt
  cairn -W prompt.txt -o result.txt
  cairn -d hello
  cairn --dict word
  cairn -F places.txt
  cairn -F places.txt -T
  cairn -F places.txt -T --travel-open
  cairn --places-file places.txt --travel
  cairn -u 123 -p "Corrected message"
  cairn -u 456 -p "New caption"           # update photo caption
  cairn -u 456 -P new.jpg -p "New caption" # replace photo and caption
`, version)
}

func main() {
	configPath := pflag.StringP("config", "c", "~/.cairn.toml", "Path to config file")
	postContent := pflag.StringP("post", "p", "", "Content to post")
	filePath := pflag.StringP("file", "f", "", "Read content from a file")
	photoPathStr := pflag.StringP("photo", "P", "", "Path to photo file(s) to post (comma-separated)")
	morning := pflag.BoolP("morning", "m", false, "Get Fitbit sleep data and post to Telegram channel")
	writerPath := pflag.StringP("writer", "W", "", "Read setting from file, send to OpenRouter (streaming), get generated content")
	outputPath := pflag.StringP("output", "o", "", "Write generated content to file (use with -W)")
	dictWord := pflag.StringP("dict", "d", "", "Look up word meaning")
	placesFile := pflag.StringP("places-file", "F", "", "Read place names to geocode, one per line (- for stdin)")
	travel := pflag.BoolP("travel", "T", false, "With -F: optimize route (great-circle); first line is start; add --travel-open for no return")
	travelOpen := pflag.Bool("travel-open", false, "With -T: open path — do not return to first place")
	updateMsgID := pflag.StringP("update", "u", "", "Message ID to update (use with -p or -f for new content)")
	help := pflag.BoolP("help", "h", false, "Show help message")

	pflag.Parse()

	if *help {
		printHelp()
		os.Exit(0)
	}

	cfgPath := *configPath
	config, err := loadConfig(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if *morning {
		if err := requireTelegram(config); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		var additionalText string
		content, file := *postContent, *filePath
		if file != "" {
			additionalText, err = readFileContent(file)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
		} else if content != "" {
			additionalText = content
		}
		if err := Morning(config, additionalText); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if *writerPath != "" {
		if err := Writer(config, *writerPath, *outputPath); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if pflag.Lookup("dict").Changed {
		word := *dictWord
		if word == "" && pflag.NArg() > 0 {
			word = pflag.Arg(0)
		}
		if word == "" {
			fmt.Fprintln(os.Stderr, "Error: -d/--dict requires a word (e.g. cairn -d hello)")
			os.Exit(1)
		}
		if err := Dict(word); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if *travel && *placesFile == "" {
		fmt.Fprintln(os.Stderr, "Error: -T/--travel requires -F/--places-file")
		os.Exit(1)
	}
	if *travelOpen && !*travel {
		fmt.Fprintln(os.Stderr, "Error: --travel-open requires -T/--travel")
		os.Exit(1)
	}

	if *placesFile != "" {
		places, err := readPlacesFromFile(*placesFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if len(places) == 0 {
			fmt.Fprintln(os.Stderr, "Error: no place names in file (use one non-comment line per place; # starts a comment)")
			os.Exit(1)
		}
		if *travel {
			if err := TravelPlacesRoute(config, places, !*travelOpen); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
		} else {
			if err := GeocodePlaces(config, places); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
		}
		return
	}

	if *updateMsgID != "" {
		if err := requireTelegram(config); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		msgID, err := strconv.ParseInt(*updateMsgID, 10, 64)
		if err != nil || msgID <= 0 {
			fmt.Fprintln(os.Stderr, "Error: -u/--update requires a positive integer message ID")
			os.Exit(1)
		}
		var updatePhotos []string
		if *photoPathStr != "" {
			for _, p := range strings.Split(*photoPathStr, ",") {
				p = strings.TrimSpace(p)
				if p != "" {
					updatePhotos = append(updatePhotos, p)
				}
			}
			for _, p := range pflag.Args() {
				p = strings.TrimSpace(p)
				if p != "" {
					updatePhotos = append(updatePhotos, p)
				}
			}
		}
		content, file := *postContent, *filePath
		if len(updatePhotos) == 1 {
			var newCaption string
			if file != "" {
				newCaption, err = readFileContent(file)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error: %v\n", err)
					os.Exit(1)
				}
			} else {
				newCaption = content
			}
			if err := editMessageMediaTelegram(config.Telegram.BotToken, config.Telegram.ChannelID, msgID, updatePhotos[0], newCaption); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		}
		if content == "" && file == "" {
			fmt.Fprintln(os.Stderr, "Error: -u/--update requires -p or -f for the new content (or -P with one photo to replace the image)")
			os.Exit(1)
		}
		if content != "" && file != "" {
			fmt.Fprintln(os.Stderr, "Error: Cannot use both --post and --file with -u")
			os.Exit(1)
		}
		var newContent string
		if file != "" {
			newContent, err = readFileContent(file)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
		} else {
			newContent = content
		}
		err = editMessageTelegram(config.Telegram.BotToken, config.Telegram.ChannelID, msgID, newContent)
		if err != nil && (strings.Contains(err.Error(), "message has no text") || strings.Contains(err.Error(), "no text in the message to edit")) {
			err = editMessageCaptionTelegram(config.Telegram.BotToken, config.Telegram.ChannelID, msgID, newContent)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	content := *postContent
	file := *filePath
	var photos []string
	if *photoPathStr != "" {
		for _, p := range strings.Split(*photoPathStr, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				photos = append(photos, p)
			}
		}
		for _, p := range pflag.Args() {
			p = strings.TrimSpace(p)
			if p != "" {
				photos = append(photos, p)
			}
		}
	}

	if len(photos) == 0 {
		if content == "" && file == "" {
			fmt.Fprintln(os.Stderr, "Error: Either --post or --file must be provided (or use -P/--photo to post a photo, -m/--morning for sleep data, -W/--writer for OpenRouter, -F/--places-file to geocode or -T with -F for a round trip, or -d/--dict)")
			printHelp()
			os.Exit(1)
		}
		if content != "" && file != "" {
			fmt.Fprintln(os.Stderr, "Error: Cannot use both --post and --file at the same time")
			os.Exit(1)
		}
	}

	var finalContent string
	if file != "" {
		finalContent, err = readFileContent(file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	} else {
		finalContent = content
	}

	if err := requireTelegram(config); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(photos) > 0 {
		if len(photos) == 1 {
			if _, err := postPhotoToTelegram(config.Telegram.BotToken, config.Telegram.ChannelID, photos[0], finalContent); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
		} else {
			if _, err := postMultiplePhotosToTelegram(config.Telegram.BotToken, config.Telegram.ChannelID, photos, finalContent); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
		}
	} else {
		if _, err := postToTelegram(config.Telegram.BotToken, config.Telegram.ChannelID, finalContent); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	}
}
