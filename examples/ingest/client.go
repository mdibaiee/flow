package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/estuary/protocols/capture"
	"github.com/estuary/protocols/flow"
	"google.golang.org/grpc"
)

func main() {
	serverAddr := "localhost:9000"
	conn, err := grpc.Dial(serverAddr, grpc.WithInsecure())
	if err != nil {
		log.Fatalf("fail to dial: %v", err)
	}
	defer conn.Close()

	client := capture.NewRuntimeClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := client.Push(ctx)

	stream.Send(&capture.PushRequest{
		Open: &capture.PushRequest_Open{
			Capture: "capture/ingest",
		},
	})

	for i := 0; i < 10; i++ {
		arena := make(flow.Arena, 1)
		docs := arena.AddAll([]byte(
			fmt.Sprintf(`{"count": %d, "message": "aaa"}`, i)))

		stream.Send(&capture.PushRequest{
			Documents: &capture.Documents{
				Binding:  0,
				Arena:    arena,
				DocsJson: docs,
			},
		})
	}

	stream.Send(&capture.PushRequest{
		Checkpoint: &flow.DriverCheckpoint{},
	})

	resp, err := stream.Recv()
	if err != nil {
		fmt.Printf("failed in receiving response, %+v", err)
	} else if err = resp.Validate(); err != nil {
		fmt.Printf("failed in validating response, %+v", err)
	} else {
		fmt.Println("receive, %+v", resp)
	}
}
