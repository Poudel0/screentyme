package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"
	"time"

	"github.com/Poudel0/screentyme/internal/sampler"
	"github.com/Poudel0/screentyme/internal/store"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.SetFlags(0)

	dbPath, err := store.DefaultPath()
	if err != nil {
		log.Fatalf("resolve db path: %v", err)
	}
	log.Printf("db: %s", dbPath)

	st, err := store.Open(dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	out := make(chan sampler.Sample, 16)
	s := sampler.New(5*time.Second, out)

	go func() {
		if err := s.Run(ctx); err != nil {
			log.Printf("sampler: %v", err)
		}
		close(out)
	}()

	for smp := range out {
		if err := st.Insert(ctx, smp); err != nil {
			log.Printf("store: insert failed: %v", err)
			continue
		}
		log.Printf(" %-20s | %s", smp.AppClass, smp.Title)
	}
	log.Println("clean shutdown")
}
