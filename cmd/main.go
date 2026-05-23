package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"
	"time"

	"github.com/Poudel0/screentyme/internal/sampler"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	out := make(chan sampler.Sample, 16)
	s := sampler.New(5*time.Second, out)

	go func() {
		if err := s.Run(ctx); err != nil {
			log.Printf("sampler: %v", err)
		}
		close(out)
	}()

	for sample := range out {
		log.Printf("%s | %-20s | %s | %s | %d | %d", sample.Timestamp.Format("15:04:05"), sample.AppClass, sample.Title, sample.ContentType, sample.Monitor, sample.PID)
	}
}
