// Command agent is Airship's data plane. It runs in your own cloud account
// or on your own host, connects out to the Airship control plane, and
// launches and destroys GitHub Actions runners there.
//
// Configuration is environment variables only:
//
//	AIRSHIP_URL              control plane URL (required)
//	AIRSHIP_TOKEN            data plane token from the dashboard (required)
//	AIRSHIP_PROVIDER         aws or docker (default aws)
//	AIRSHIP_CAPACITY         launches accepted per sync (default 10)
//
//	AWS provider (credentials from the standard AWS chain, e.g. a task role):
//	AIRSHIP_AWS_SUBNETS          comma-separated subnet IDs (required)
//	AIRSHIP_AWS_SECURITY_GROUPS  comma-separated security group IDs
//	AIRSHIP_AWS_INSTANCE_PROFILE instance profile name for runners (optional)
//	AIRSHIP_AWS_IMAGE_X64        default x64 AMI or resolve:ssm:<parameter>
//	AIRSHIP_AWS_IMAGE_ARM64      default arm64 AMI or resolve:ssm:<parameter>
//	AIRSHIP_AWS_SPOT_FALLBACK    launch on-demand when spot is unavailable (default true)
//	AIRSHIP_AWS_METADATA_HOP_LIMIT IMDSv2 hop limit for runners (default 1)
//
//	Docker provider:
//	AIRSHIP_DOCKER_SOCKET    Docker Engine socket (default /var/run/docker.sock)
//	AIRSHIP_DOCKER_NETWORK   network for runner containers
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/wizenheimer/airship/apps/api/internal/agent"
	agentaws "github.com/wizenheimer/airship/apps/api/internal/agent/aws"
	"github.com/wizenheimer/airship/apps/api/internal/agent/docker"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/caarlos0/env/v11"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

type settings struct {
	URL      string `env:"AIRSHIP_URL,required,notEmpty"`
	Token    string `env:"AIRSHIP_TOKEN,required,notEmpty"`
	Provider string `env:"AIRSHIP_PROVIDER" envDefault:"aws"`
	Capacity int    `env:"AIRSHIP_CAPACITY" envDefault:"10"`

	AWSSubnets         []string `env:"AIRSHIP_AWS_SUBNETS"          envSeparator:","`
	AWSSecurityGroups  []string `env:"AIRSHIP_AWS_SECURITY_GROUPS"  envSeparator:","`
	AWSInstanceProfile string   `env:"AIRSHIP_AWS_INSTANCE_PROFILE"`
	AWSImageX64        string   `env:"AIRSHIP_AWS_IMAGE_X64"`
	AWSImageARM64      string   `env:"AIRSHIP_AWS_IMAGE_ARM64"`
	AWSSpotFallback    bool     `env:"AIRSHIP_AWS_SPOT_FALLBACK"    envDefault:"true"`
	AWSHopLimit        int32    `env:"AIRSHIP_AWS_METADATA_HOP_LIMIT" envDefault:"1"`

	DockerSocket  string `env:"AIRSHIP_DOCKER_SOCKET"`
	DockerNetwork string `env:"AIRSHIP_DOCKER_NETWORK"`
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("agent stopped", slog.Any("error", err))
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := env.ParseAs[settings]()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if !strings.HasPrefix(cfg.URL, "https://") && !strings.HasPrefix(cfg.URL, "http://localhost") && !strings.HasPrefix(cfg.URL, "http://127.0.0.1") {
		return errors.New("AIRSHIP_URL must use https: the agent sends its token and receives runner credentials")
	}

	provider, err := newProvider(ctx, cfg)
	if err != nil {
		return err
	}
	logger.Info("starting agent", slog.String("version", version), slog.String("provider", provider.Name()), slog.String("control_plane", cfg.URL))

	client := agent.NewClient(cfg.URL, cfg.Token, nil)

	return agent.New(client, provider, agent.Config{Capacity: cfg.Capacity, Version: version}, logger).Run(ctx) //nolint:wrapcheck // final error
}

func newProvider(ctx context.Context, cfg settings) (agent.Provider, error) {
	switch cfg.Provider {
	case "docker":
		return docker.New(docker.Config{Socket: cfg.DockerSocket, Network: cfg.DockerNetwork}), nil
	case "aws":
		awsCfg, err := config.LoadDefaultConfig(ctx)
		if err != nil {
			return nil, fmt.Errorf("load aws config: %w", err)
		}
		images := map[string]string{}
		for arch, image := range agentaws.DefaultImages {
			images[arch] = image
		}
		if cfg.AWSImageX64 != "" {
			images["x64"] = cfg.AWSImageX64
		}
		if cfg.AWSImageARM64 != "" {
			images["arm64"] = cfg.AWSImageARM64
		}
		provider, err := agentaws.New(ec2.NewFromConfig(awsCfg), ssm.NewFromConfig(awsCfg), agentaws.Config{
			Subnets: cfg.AWSSubnets, SecurityGroups: cfg.AWSSecurityGroups, InstanceProfile: cfg.AWSInstanceProfile,
			DefaultImages: images, SpotFallback: cfg.AWSSpotFallback, MetadataHopLimit: cfg.AWSHopLimit,
		})
		if err != nil {
			return nil, fmt.Errorf("configure aws provider: %w", err)
		}

		return provider, nil
	default:
		return nil, fmt.Errorf("unknown AIRSHIP_PROVIDER %q (want aws or docker)", cfg.Provider)
	}
}
