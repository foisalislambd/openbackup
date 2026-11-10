package httpapi

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/foisalislambd/openbackup/internal/server/config"
	"golang.org/x/sync/semaphore"
)

func TestWidenRejectsPlaintextBeyondTheBudget(t *testing.T) {
	s := &Server{
		cfg:          config.Config{UploadBudgetBytes: 1000},
		uploadBudget: semaphore.NewWeighted(1000),
	}
	req, err := http.NewRequest(http.MethodPut, "http://example/chunk", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = 100
	release, held, err := s.admitUpload(req, 1000)
	if err != nil {
		t.Fatal(err)
	}
	rel, _, err := s.widenAdmission(context.Background(), release, held, 100, 50_000)
	if !errors.Is(err, errUploadTooLarge) {
		t.Fatalf("err=%v", err)
	}
	rel()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.uploadBudget.Acquire(ctx, 1000); err != nil {
		t.Fatalf("reservation leaked: %v", err)
	}
}

func TestWidenDoesNotDeadlockWhenTheRequestAlreadyHoldsTheBudget(t *testing.T) {
	s := &Server{
		cfg:          config.Config{UploadBudgetBytes: 1000},
		uploadBudget: semaphore.NewWeighted(1000),
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, "http://example/batch", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = -1
	release, held, err := s.admitUpload(req, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if held != 1000 {
		t.Fatalf("unknown length held %d, want the whole budget", held)
	}
	rel, got, err := s.widenAdmission(ctx, release, held, 100, 800)
	if err != nil || got != held {
		t.Fatal(err)
	}
	rel()
	if err := s.uploadBudget.Acquire(ctx, 1000); err != nil {
		t.Fatalf("budget not released: %v", err)
	}
}

func TestCancelledAdmissionDoesNotKeepTheBudget(t *testing.T) {
	s := &Server{
		cfg:          config.Config{UploadBudgetBytes: 100},
		uploadBudget: semaphore.NewWeighted(100),
	}
	if err := s.uploadBudget.Acquire(context.Background(), 90); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, "http://example/chunk", nil)
		if err != nil {
			done <- err
			return
		}
		req.ContentLength = 50
		_, _, err = s.admitUpload(req, 100)
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	if err := <-done; err == nil {
		t.Fatal("cancelled admission succeeded")
	}
	s.uploadBudget.Release(90)
	ctx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if err := s.uploadBudget.Acquire(ctx, 100); err != nil {
		t.Fatalf("cancel leaked a reservation: %v", err)
	}
}
