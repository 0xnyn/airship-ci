package console

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/auth"
	"github.com/wizenheimer/airship/apps/api/internal/cloud"
	awscloud "github.com/wizenheimer/airship/apps/api/internal/cloud/aws"
	gcpcloud "github.com/wizenheimer/airship/apps/api/internal/cloud/gcp"
	"github.com/wizenheimer/airship/apps/api/internal/db"
	"github.com/wizenheimer/airship/apps/api/internal/fleet"

	"github.com/danielgtaylor/huma/v2"
)

// DefaultRegion is where runners launch when neither the config nor the
// credentials say; Cirun's default.
const DefaultRegion = "eu-west-1"

// Cloud is a connected cloud account, without its secret.
type Cloud struct {
	Cloud         string     `doc:"Cloud name"                                                               enum:"aws,gcp" json:"cloud"`
	Identity      string     `doc:"Public half of the credential (AWS access key ID, GCP service account email)" json:"identity"`
	AccountID     *string    `doc:"Cloud account ID"                              json:"accountId"`
	Principal     *string    `doc:"Who the credential is, such as an IAM ARN"     json:"principal"`
	DefaultRegion string     `doc:"Region (or GCP zone) used when a config names none" json:"defaultRegion"`
	Status        string     `doc:"Whether the credential works"                  enum:"ok,invalid" json:"status"`
	Error         *string    `doc:"Why the credential stopped working"            json:"error"`
	VerifiedAt    *time.Time `doc:"When it was last verified with the cloud"      json:"verifiedAt"`
	ConnectedAt   time.Time  `doc:"When it was connected"                         json:"connectedAt"`
	UpdatedAt     time.Time  `doc:"When it was last changed"                      json:"updatedAt"`
}

func cloudFromDB(c db.CloudCredential) Cloud {
	return Cloud{
		Cloud: c.Cloud, Identity: c.Identity, AccountID: c.AccountID, Principal: c.Principal, DefaultRegion: c.DefaultRegion,
		Status: c.Status, Error: c.Error, VerifiedAt: c.VerifiedAt, ConnectedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
}

// CloudList is every cloud connected to an organization.
type CloudList struct {
	Items []Cloud `json:"items"`
}

type cloudListOutput struct {
	Body CloudList
}

func (h *handlers) listClouds(ctx context.Context, in *orgInput) (*cloudListOutput, error) {
	inst, err := h.org(ctx, in.Org, false)
	if err != nil {
		return nil, err
	}
	rows, err := h.Store.ListCloudCredentials(ctx, inst.ID)
	if err != nil {
		return nil, fmt.Errorf("list clouds: %w", err)
	}
	items := make([]Cloud, 0, len(rows))
	for _, row := range rows {
		items = append(items, cloudFromDB(row))
	}

	return &cloudListOutput{Body: CloudList{Items: items}}, nil
}

// ConnectAWS is the request body for connecting an AWS account: an IAM
// user's access key, as Cirun asks for it.
type ConnectAWS struct {
	AccessKeyID     string `doc:"IAM access key ID"                                 json:"accessKeyId" maxLength:"128" minLength:"16" pattern:"^[A-Z0-9]+$"`
	SecretAccessKey string `doc:"IAM secret access key"                             json:"secretAccessKey" maxLength:"128" minLength:"16"`
	DefaultRegion   string `default:"eu-west-1" doc:"Region used when a config names none" json:"defaultRegion,omitempty" maxLength:"64" pattern:"^[a-z]{2}(-[a-z]+)+-[0-9]$"`
}

type connectAWSInput struct {
	Org  string `maxLength:"100" minLength:"1" path:"org"`
	Body ConnectAWS
}

type cloudOutput struct {
	Body Cloud
}

func (h *handlers) connectAWS(ctx context.Context, in *connectAWSInput) (*cloudOutput, error) {
	inst, err := h.org(ctx, in.Org, true)
	if err != nil {
		return nil, err
	}
	user, _ := auth.UserFrom(ctx)
	region := in.Body.DefaultRegion
	if region == "" {
		region = DefaultRegion
	}
	secret, err := awscloud.EncodeSecret(awscloud.Secret{AccessKeyID: in.Body.AccessKeyID, SecretAccessKey: in.Body.SecretAccessKey})
	if err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}
	row, err := h.connect(ctx, inst.ID, cloud.AWS, secret, region, &user.ID)
	if err != nil {
		return nil, err
	}

	return &cloudOutput{Body: cloudFromDB(row)}, nil
}

// ConnectGCP is the request body for connecting a Google Cloud project: a
// service account's JSON key, pasted whole, as Cirun asks for it.
type ConnectGCP struct {
	ServiceAccountKey string `doc:"The service account key file's JSON, as downloaded from Google Cloud" json:"serviceAccountKey" maxLength:"16384" minLength:"2"`
	DefaultRegion     string `default:"us-central1" doc:"Zone (us-central1-b) or region (us-central1) used when a config names none" json:"defaultRegion,omitempty" maxLength:"64" pattern:"^[a-z]+-[a-z]+[0-9]+(-[a-z])?$"`
}

type connectGCPInput struct {
	Org  string `maxLength:"100" minLength:"1" path:"org"`
	Body ConnectGCP
}

func (h *handlers) connectGCP(ctx context.Context, in *connectGCPInput) (*cloudOutput, error) {
	inst, err := h.org(ctx, in.Org, true)
	if err != nil {
		return nil, err
	}
	user, _ := auth.UserFrom(ctx)
	region := in.Body.DefaultRegion
	if region == "" {
		region = gcpcloud.DefaultRegion
	}
	secret, err := gcpcloud.EncodeSecret([]byte(in.Body.ServiceAccountKey))
	if err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}
	row, err := h.connect(ctx, inst.ID, cloud.GCP, secret, region, &user.ID)
	if err != nil {
		return nil, err
	}

	return &cloudOutput{Body: cloudFromDB(row)}, nil
}

// connect verifies and stores credentials, turning the cloud's refusal
// into a 422 the dashboard can show.
func (h *handlers) connect(ctx context.Context, installationID int64, cloudName string, secret []byte, region string, by *uuidPtr) (db.CloudCredential, error) {
	row, err := h.Fleet.ConnectCloud(ctx, installationID, cloudName, secret, region, by)
	if errors.Is(err, cloud.ErrBadCredentials) {
		return db.CloudCredential{}, huma.Error422UnprocessableEntity(err.Error())
	}
	if errors.Is(err, cloud.ErrUnsupportedCloud) {
		return db.CloudCredential{}, huma.Error422UnprocessableEntity("only aws and gcp are supported")
	}
	if err != nil {
		slog.WarnContext(ctx, "verify cloud credentials", slog.String("cloud", cloudName), slog.Any("error", err))

		return db.CloudCredential{}, huma.Error502BadGateway("could not verify the credentials with " + cloudName + "; try again in a moment")
	}

	return row, nil
}

type cloudInput struct {
	Org   string `maxLength:"100" minLength:"1" path:"org"`
	Cloud string `doc:"Cloud name" enum:"aws,gcp" path:"cloud"`
}

func (h *handlers) disconnectCloud(ctx context.Context, in *cloudInput) (*struct{}, error) {
	inst, err := h.org(ctx, in.Org, true)
	if err != nil {
		return nil, err
	}
	err = h.Fleet.DisconnectCloud(ctx, inst.ID, in.Cloud)
	switch {
	case errors.Is(err, fleet.ErrCloudNotFound):
		return nil, huma.Error404NotFound("no credentials for " + in.Cloud)
	case errors.Is(err, fleet.ErrCloudInUse):
		return nil, huma.Error409Conflict("runners launched with these credentials are still alive; wait for them to finish")
	case err != nil:
		return nil, fmt.Errorf("disconnect cloud: %w", err)
	}

	return &struct{}{}, nil
}
