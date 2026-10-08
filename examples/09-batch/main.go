// Official message batches for OpenAI and Anthropic.
//
//	OPENAI_API_KEY=sk-... go run .
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/ncobase/deebus"
)

func main() {
	client, err := deebus.NewClient(deebus.Config{
		Providers: map[string]deebus.ProviderConfig{
			"openai": {Type: "openai", APIKey: os.Getenv("OPENAI_API_KEY")},
		},
		Primary: "openai/gpt-4o",
	})
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()
	batch, err := client.SubmitBatch(ctx, &deebus.BatchRequest{
		Provider: "openai",
		Items: []deebus.BatchItem{{
			CustomID: "greeting",
			Request: &deebus.Request{
				Model:    "gpt-4o",
				Messages: []deebus.Message{deebus.TextMessage("user", "Say hello in one sentence.")},
			},
		}},
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("batch=%s status=%s\n", batch.ID, batch.Status)

	deadline := time.Now().Add(2 * time.Minute)
	for batch.Status != deebus.OperationSucceeded && batch.Status != deebus.OperationFailed && time.Now().Before(deadline) {
		time.Sleep(10 * time.Second)
		batch, err = client.GetBatch(ctx, "openai", batch.ID)
		if err != nil {
			log.Fatal(err)
		}
	}
	if batch.Status != deebus.OperationSucceeded {
		log.Fatalf("batch status=%s error=%s", batch.Status, batch.Error)
	}
	results, err := client.ReadBatch(ctx, "openai", batch.ID)
	if err != nil {
		log.Fatal(err)
	}
	for _, result := range results {
		fmt.Printf("%s: %s\n", result.CustomID, result.Content)
	}
}
