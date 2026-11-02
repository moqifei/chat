package chat

import (
	"context"
	"sync"

	"github.com/openimsdk/tools/log"
)

type organizationSyncScope struct {
	AD              bool
	DigitalEmployee bool
}

type organizationSyncResult struct {
	ADErr              error
	DigitalEmployeeErr error
}

func executeOrganizationSync(
	ctx context.Context,
	mu *sync.Mutex,
	scope organizationSyncScope,
	runAD func(context.Context) error,
	runDigitalEmployee func(context.Context) error,
) organizationSyncResult {
	mu.Lock()
	defer mu.Unlock()

	result := organizationSyncResult{}
	if scope.AD && runAD != nil {
		result.ADErr = runAD(ctx)
	}
	if scope.DigitalEmployee && runDigitalEmployee != nil {
		result.DigitalEmployeeErr = runDigitalEmployee(ctx)
	}
	return result
}

func enqueueOrganizationSync(run func(context.Context)) {
	go run(syncBackgroundCtx())
}

func (o *chatSvr) runOrganizationSync(ctx context.Context, scope organizationSyncScope) {
	result := executeOrganizationSync(
		ctx,
		&o.organizationSyncMu,
		scope,
		func(ctx context.Context) error {
			if o.ADClient == nil || !o.ADConfig.Sync.Enable {
				return nil
			}
			return o.runADSync(ctx)
		},
		func(ctx context.Context) error {
			if !o.DigitalEmployeeConfig.Enable {
				return nil
			}
			return o.runDigitalEmployeeSync(ctx)
		},
	)
	if result.ADErr != nil {
		log.ZError(ctx, "organization sync: AD phase failed", result.ADErr)
	}
	if result.DigitalEmployeeErr != nil {
		log.ZError(ctx, "organization sync: digital employee phase failed", result.DigitalEmployeeErr)
	}
}

func (o *chatSvr) queueOrganizationSync(scope organizationSyncScope) {
	enqueueOrganizationSync(func(ctx context.Context) {
		o.runOrganizationSync(ctx, scope)
	})
}
