// Image generation, speech, and transcription through one client.
//
//	OPENAI_API_KEY=sk-... go run .
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/ncobase/deebus"
)

func main() {
	client, err := deebus.NewClient(deebus.Config{
		Providers: map[string]deebus.ProviderConfig{
			"openai": {
				Type:    "openai",
				APIKey:  os.Getenv("OPENAI_API_KEY"),
				BaseURL: "https://api.openai.com",
			},
		},
		Primary: "openai/gpt-4o",
	})
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()
	image, err := client.GenerateImage(ctx, &deebus.ImageRequest{
		Model:          "openai/gpt-image-1",
		Prompt:         "a small red boat on a calm lake",
		Size:           "1024x1024",
		ResponseFormat: "b64_json",
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("images=%d bytes=%d\n", len(image.Images), len(image.Images[0].B64JSON))

	speech, err := client.SynthesizeSpeech(ctx, &deebus.SpeechRequest{
		Model: "openai/tts-1",
		Input: "deebus speaks through one client.",
		Voice: "alloy",
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("audio=%s bytes=%d\n", speech.MediaType, len(speech.Audio))
}
