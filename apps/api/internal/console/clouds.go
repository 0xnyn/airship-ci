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
	azurecloud "github.com/wizenheimer/airship/apps/api/internal/cloud/azure"
	daytonacloud "github.com/wizenheimer/airship/apps/api/internal/cloud/daytona"
	gcpcloud "github.com/wizenheimer/airship/apps/api/internal/cloud/gcp"
	"github.com/wizenheimer/airship/apps/api/internal/db"
	"github.com/wizenheimer/airship/apps/api/internal/fleet"

	"github.com/danielgtaylor/huma/v2"
)

// DefaultRegion is where runners launch when neither the config nor the
// credentials say.
const DefaultRegion = "eu-west-1"

// Cloud is a connected cloud account, without its secret.
type Cloud struct {
	Cloud         string     `doc:"Cloud name"                                                               enum:"aws,gcp,azure,daytona" json:"cloud"`
	Identity      string     `doc:"Public half of the credential (AWS access key ID, GCP service account email, Azure application ID, masked Daytona API key)" json:"identity"`
	AccountID     *string    `doc:"Cloud account ID (AWS account, GCP project, Azure subscription, Daytona organization)" json:"accountId"`
	Principal     *string    `doc:"Who the credential is, such as an IAM ARN or an API key's name"     json:"principal"`
	DefaultRegion string     `doc:"Region (or GCP zone, or Daytona region) used when a config names none" json:"defaultRegion"`
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
// user's access key.
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
// service account's JSON key, pasted whole.
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

// ConnectDaytona is the request body for connecting a Daytona organization:
// an API key with sandbox permissions.
type ConnectDaytona struct {
	APIKey        string `doc:"Organization API key (write:sandboxes and delete:sandboxes)" json:"apiKey" maxLength:"512" minLength:"16"`
	APIURL        string `doc:"API URL, for a self-hosted Daytona; the hosted service by default" format:"uri" json:"apiUrl,omitempty" maxLength:"256"`
	DefaultRegion string `default:"us" doc:"Region used when a config names none" json:"defaultRegion,omitempty" maxLength:"63" pattern:"^[a-z0-9][a-z0-9-]{0,62}$"`
}

type connectDaytonaInput struct {
	Org  string `maxLength:"100" minLength:"1" path:"org"`
	Body ConnectDaytona
}

func (h *handlers) connectDaytona(ctx context.Context, in *connectDaytonaInput) (*cloudOutput, error) {
	inst, err := h.org(ctx, in.Org, true)
	if err != nil {
		return nil, err
	}
	user, _ := auth.UserFrom(ctx)
	region := in.Body.DefaultRegion
	if region == "" {
		region = daytonacloud.DefaultRegion
	}
	secret, err := daytonacloud.EncodeSecret(daytonacloud.Secret{APIKey: in.Body.APIKey, APIURL: in.Body.APIURL})
	if err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}
	row, err := h.connect(ctx, inst.ID, cloud.Daytona, secret, region, &user.ID)
	if err != nil {
		return nil, err
	}

	return &cloudOutput{Body: cloudFromDB(row)}, nil
}

// ConnectAzure is the request body for connecting an Azure subscription: a
// Microsoft Entra application (service principal) with the Contributor
// role on the subscription.
type ConnectAzure struct {
	SubscriptionID string `doc:"Subscription ID" format:"uuid" json:"subscriptionId"`
	TenantID       string `doc:"Directory (tenant) ID of the application" format:"uuid" json:"tenantId"`
	ClientID       string `doc:"Application (client) ID" format:"uuid" json:"clientId"`
	ClientSecret   string `doc:"A client secret of the application" json:"clientSecret" maxLength:"1024" minLength:"1"`
	DefaultRegion  string `default:"eastus" doc:"Region (eastus, westeurope) used when a config names none" json:"defaultRegion,omitempty" maxLength:"40" pattern:"^[a-z][a-z0-9]{2,39}$"`
}

type connectAzureInput struct {
	Org  string `maxLength:"100" minLength:"1" path:"org"`
	Body ConnectAzure
}

func (h *handlers) connectAzure(ctx context.Context, in *connectAzureInput) (*cloudOutput, error) {
	inst, err := h.org(ctx, in.Org, true)
	if err != nil {
		return nil, err
	}
	user, _ := auth.UserFrom(ctx)
	region := in.Body.DefaultRegion
	if region == "" {
		region = azurecloud.DefaultRegion
	}
	secret, err := azurecloud.EncodeSecret(azurecloud.Secret{
		SubscriptionID: in.Body.SubscriptionID, TenantID: in.Body.TenantID, ClientID: in.Body.ClientID, ClientSecret: in.Body.ClientSecret,
	})
	if err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}
	row, err := h.connect(ctx, inst.ID, cloud.Azure, secret, region, &user.ID)
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
		return db.CloudCredential{}, huma.Error422UnprocessableEntity("only aws, gcp, azure and daytona are supported")
	}
	if err != nil {
		slog.WarnContext(ctx, "verify cloud credentials", slog.String("cloud", cloudName), slog.Any("error", err))

		return db.CloudCredential{}, huma.Error502BadGateway("could not verify the credentials with " + cloudName + "; try again in a moment")
	}

	return row, nil
}

type cloudInput struct {
	Org   string `maxLength:"100" minLength:"1" path:"org"`
	Cloud string `doc:"Cloud name" enum:"aws,gcp,azure,daytona" path:"cloud"`
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
