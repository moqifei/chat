package chat

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestExecuteOrganizationSyncRunsDigitalEmployeeAfterADFailure(t *testing.T) {
	var mu sync.Mutex
	adErr := errors.New("LDAP unavailable")
	calls := make([]string, 0, 2)

	result := executeOrganizationSync(
		context.Background(),
		&mu,
		organizationSyncScope{AD: true, DigitalEmployee: true},
		func(context.Context) error {
			calls = append(calls, "ad")
			return adErr
		},
		func(context.Context) error {
			calls = append(calls, "digital")
			return nil
		},
	)

	if !errors.Is(result.ADErr, adErr) {
		t.Fatalf("AD error = %v, want %v", result.ADErr, adErr)
	}
	if result.DigitalEmployeeErr != nil {
		t.Fatalf("digital employee error = %v, want nil", result.DigitalEmployeeErr)
	}
	if want := []string{"ad", "digital"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
}

func TestExecuteOrganizationSyncSerializesConcurrentRuns(t *testing.T) {
	var mu sync.Mutex
	firstEntered := make(chan struct{}, 1)
	releaseFirst := make(chan struct{})
	secondEntered := make(chan struct{}, 1)

	go func() {
		executeOrganizationSync(
			context.Background(),
			&mu,
			organizationSyncScope{AD: true},
			func(context.Context) error {
				firstEntered <- struct{}{}
				<-releaseFirst
				return nil
			},
			nil,
		)
	}()
	<-firstEntered

	go func() {
		executeOrganizationSync(
			context.Background(),
			&mu,
			organizationSyncScope{AD: true},
			func(context.Context) error {
				secondEntered <- struct{}{}
				return nil
			},
			nil,
		)
	}()

	select {
	case <-secondEntered:
		t.Fatal("second synchronization entered before first synchronization completed")
	case <-time.After(30 * time.Millisecond):
	}

	close(releaseFirst)
	select {
	case <-secondEntered:
	case <-time.After(time.Second):
		t.Fatal("second synchronization did not run after first synchronization completed")
	}
}

func TestEnqueueOrganizationSyncUsesBackgroundContext(t *testing.T) {
	completed := make(chan context.Context, 1)
	enqueueOrganizationSync(func(ctx context.Context) {
		completed <- ctx
	})

	select {
	case ctx := <-completed:
		if ctx.Err() != nil {
			t.Fatal("background synchronization context is cancelled")
		}
	case <-time.After(time.Second):
		t.Fatal("queued synchronization did not start")
	}
}
