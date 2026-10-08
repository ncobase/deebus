// Official long-running image and video tasks.
//
//	VOLC_ACCESS_KEY=... VOLC_SECRET_KEY=... go run .
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
			"jimeng": {
				Type:      "jimeng",
				AccessKey: os.Getenv("VOLC_ACCESS_KEY"),
				Secret:    os.Getenv("VOLC_SECRET_KEY"),
			},
		},
		Primary: "jimeng/jimeng_t2i_v40",
	})
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()
	op, err := client.Submit(ctx, &deebus.OperationRequest{
		Model:  "jimeng/jimeng_t2i_v40",
		Kind:   deebus.OperationImage,
		Prompt: "a quiet lake at dawn",
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("submitted provider=%s id=%s\n", op.Provider, op.ID)

	deadline := time.Now().Add(2 * time.Minute)
	for op.Status != deebus.OperationSucceeded && op.Status != deebus.OperationFailed && time.Now().Before(deadline) {
		time.Sleep(5 * time.Second)
		op, err = client.GetOperation(ctx, op.Provider, op.ID)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("status=%s\n", op.Status)
	}
}
