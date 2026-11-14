package modal

import (
	"context"
	"errors"
	"fmt"

	modalsdk "github.com/modal-labs/modal-client/go"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// sdk is API over Modal's Go SDK. One is made per provider call, around a
// client of its own, and closed after.
type sdk struct {
	client      *modalsdk.Client
	environment string
}

func connectSDK(secret Secret) (API, error) {
	client, err := modalsdk.NewClientWithOptions(&modalsdk.ClientParams{
		TokenID:     secret.TokenID,
		TokenSecret: secret.TokenSecret,
		Environment: secret.Environment,
	})
	if err != nil {
		return nil, mapErr(err)
	}

	return &sdk{client: client, environment: secret.Environment}, nil
}

// mapErr sorts the SDK's errors into the API's.
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	var notFound modalsdk.NotFoundError
	var exists modalsdk.AlreadyExistsError
	switch {
	case errors.As(err, &notFound):
		return fmt.Errorf("%w: %w", ErrNotFound, err)
	case errors.As(err, &exists):
		return fmt.Errorf("%w: %w", ErrAlreadyExists, err)
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("%w: %w", ErrTransient, err)
	}
	switch status.Code(err) {
	case codes.NotFound:
		return fmt.Errorf("%w: %w", ErrNotFound, err)
	case codes.AlreadyExists:
		return fmt.Errorf("%w: %w", ErrAlreadyExists, err)
	case codes.Unauthenticated, codes.PermissionDenied:
		return fmt.Errorf("%w: %w", ErrUnauthorized, err)
	case codes.Unavailable, codes.ResourceExhausted, codes.DeadlineExceeded, codes.Aborted:
		return fmt.Errorf("%w: %w", ErrTransient, err)
	case codes.OK, codes.Canceled, codes.Unknown, codes.InvalidArgument, codes.FailedPrecondition, codes.OutOfRange,
		codes.Unimplemented, codes.Internal, codes.DataLoss:
		// Permanent, or not a gRPC status at all: returned as is.
	}

	return err
}

func (a *sdk) App(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	app, err := a.client.Apps.FromName(ctx, AppName, &modalsdk.AppFromNameParams{Environment: a.environment, CreateIfMissing: true})
	if err != nil {
		return "", mapErr(err)
	}

	return app.AppID, nil
}

func (a *sdk) Create(ctx context.Context, spec Spec) (string, error) {
	app, err := a.client.Apps.FromName(ctx, AppName, &modalsdk.AppFromNameParams{Environment: a.environment, CreateIfMissing: true})
	if err != nil {
		return "", mapErr(err)
	}
	params := &modalsdk.SandboxCreateParams{
		CPU:       spec.CPU,
		MemoryMiB: spec.MemoryMB,
		GPU:       spec.GPU,
		Timeout:   spec.Timeout,
		Command:   spec.Command,
		Name:      spec.Name,
		Tags:      spec.Tags,
	}
	if spec.VM {
		params.Runtime = modalsdk.SandboxRuntimeVM
	}
	sb, err := a.client.Sandboxes.Create(ctx, app, a.client.Images.FromRegistry(spec.Image, nil), params)
	if err != nil {
		return "", mapErr(err)
	}
	defer func() { _ = sb.Detach() }()

	return sb.SandboxID, nil
}

func (a *sdk) FromName(ctx context.Context, name string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	sb, err := a.client.Sandboxes.FromName(ctx, AppName, name, &modalsdk.SandboxFromNameParams{Environment: a.environment})
	if err != nil {
		return "", mapErr(err)
	}
	defer func() { _ = sb.Detach() }()

	return sb.SandboxID, nil
}

func (a *sdk) sandbox(ctx context.Context, id string) (*modalsdk.Sandbox, error) {
	sb, err := a.client.Sandboxes.FromID(ctx, id, nil)
	if err != nil {
		return nil, mapErr(err)
	}

	return sb, nil
}

func (a *sdk) Tags(ctx context.Context, id string) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	sb, err := a.sandbox(ctx, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = sb.Detach() }()
	tags, err := sb.GetTags(ctx, nil)

	return tags, mapErr(err)
}

func (a *sdk) List(ctx context.Context, appID string, tags map[string]string) ([]string, error) {
	seq, err := a.client.Sandboxes.List(ctx, &modalsdk.SandboxListParams{AppID: appID, Tags: tags, Environment: a.environment})
	if err != nil {
		return nil, mapErr(err)
	}
	var ids []string
	for sb, err := range seq {
		if err != nil {
			return nil, mapErr(err)
		}
		ids = append(ids, sb.SandboxID)
		_ = sb.Detach()
	}

	return ids, nil
}

func (a *sdk) Exec(ctx context.Context, id string, argv []string, env map[string]string) (int, error) {
	sb, err := a.sandbox(ctx, id)
	if err != nil {
		return 0, err
	}
	defer func() { _ = sb.Detach() }()
	proc, err := sb.Exec(ctx, argv, &modalsdk.SandboxExecParams{
		Stdout:  modalsdk.Ignore,
		Stderr:  modalsdk.Ignore,
		Timeout: execTimeout,
		Env:     env,
	})
	if err != nil {
		return 0, mapErr(err)
	}
	code, err := proc.Wait(ctx, nil)

	return code, mapErr(err)
}

func (a *sdk) Terminate(ctx context.Context, id string) error {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	sb, err := a.sandbox(ctx, id)
	if err != nil {
		return err
	}
	_, err = sb.Terminate(ctx, nil)

	return mapErr(err)
}

func (a *sdk) Close() { a.client.Close() }
