package console

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/db"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
)

const overviewWindow = 24 * time.Hour

// Overview summarizes an organization's last 24 hours.
type Overview struct {
	Jobs            int64        `doc:"Jobs queued in the window"                       json:"jobs"`
	AirshipJobs     int64        `doc:"Of those, jobs that ran on Airship"             json:"airshipJobs"`
	Queued          int64        `doc:"Jobs still waiting"                              json:"queued"`
	InProgress      int64        `doc:"Jobs running now"                                json:"inProgress"`
	Succeeded       int64        `doc:"Jobs that succeeded"                             json:"succeeded"`
	Failed          int64        `doc:"Jobs that failed"                                json:"failed"`
	QueueP50Seconds float64      `doc:"Median wait from queued to started on Airship"  json:"queueP50Seconds"`
	QueueP90Seconds float64      `doc:"90th percentile wait from queued to started"    json:"queueP90Seconds"`
	RunnerMinutes   float64      `doc:"Minutes of jobs run on Airship"                 json:"runnerMinutes"`
	Runners         RunnerCounts `json:"runners"`
}

// RunnerCounts are runners in each live state.
type RunnerCounts struct {
	Pending  int64 `doc:"Waiting for capacity"           json:"pending"`
	Starting int64 `doc:"Being launched"                  json:"starting"`
	Idle     int64 `doc:"Online, waiting for a job"       json:"idle"`
	Busy     int64 `doc:"Running a job"                   json:"busy"`
}

type overviewOutput struct {
	Body Overview
}

func (h *handlers) overview(ctx context.Context, in *orgInput) (*overviewOutput, error) {
	inst, err := h.org(ctx, in.Org, false)
	if err != nil {
		return nil, err
	}
	stats, err := h.Store.JobStats(ctx, db.JobStatsParams{InstallationID: inst.ID, Since: time.Now().Add(-overviewWindow)})
	if err != nil {
		return nil, fmt.Errorf("job stats: %w", err)
	}
	counts, err := h.Store.CountRunnersByState(ctx, inst.ID)
	if err != nil {
		return nil, fmt.Errorf("count runners: %w", err)
	}
	var runners RunnerCounts
	for _, count := range counts {
		switch count.State {
		case "pending":
			runners.Pending = count.Runners
		case "launching":
			runners.Starting = count.Runners
		case "running":
			runners.Idle = count.Runners
		case "busy":
			runners.Busy = count.Runners
		}
	}

	return &overviewOutput{Body: Overview{
		Jobs: stats.Jobs, AirshipJobs: stats.AirshipJobs, Queued: stats.Queued, InProgress: stats.InProgress,
		Succeeded: stats.Succeeded, Failed: stats.Failed, QueueP50Seconds: stats.QueueP50Seconds,
		QueueP90Seconds: stats.QueueP90Seconds, RunnerMinutes: stats.RunnerSeconds / 60, Runners: runners,
	}}, nil
}

// Job is a GitHub Actions job Airship saw.
type Job struct {
	ID           int64      `doc:"GitHub job ID"                                      json:"id"`
	Repository   string     `doc:"Repository (owner/name)"                            json:"repository"`
	WorkflowName *string    `doc:"Workflow name"                                      json:"workflowName"`
	Name         string     `doc:"Job name"                                           json:"name"`
	HeadBranch   *string    `doc:"Branch the job ran for"                             json:"headBranch"`
	HTMLURL      *string    `doc:"Link to the job on GitHub"                          json:"htmlUrl"`
	Labels       []string   `doc:"runs-on labels"                                     json:"labels"`
	Status       string     `doc:"Job status"                                         enum:"queued,in_progress,completed" json:"status"`
	Conclusion   *string    `doc:"Result once completed"                              json:"conclusion"`
	SkipReason   *string    `doc:"Why Airship did not run a job that asked for it"    enum:"public_repository,unsatisfiable_labels,installation_suspended,repository_inactive,config_invalid,access_denied,no_cloud" json:"skipReason"`
	SkipDetail   *string    `doc:"The skip explained, for config file runners"        json:"skipDetail"`
	RunnerName   *string    `doc:"Runner GitHub ran the job on"                       json:"runnerName"`
	OnAirship    bool       `doc:"Whether the job ran on an Airship runner"           json:"onAirship"`
	QueuedAt     time.Time  `doc:"When the job was queued"                            json:"queuedAt"`
	StartedAt    *time.Time `doc:"When it started"                                    json:"startedAt"`
	CompletedAt  *time.Time `doc:"When it completed"                                  json:"completedAt"`
}

func jobFromDB(j db.Job) Job {
	return Job{
		ID: j.ID, Repository: j.RepositoryFullName, WorkflowName: j.WorkflowName, Name: j.Name,
		HeadBranch: j.HeadBranch, HTMLURL: j.HtmlUrl, Labels: j.Labels, Status: j.Status,
		Conclusion: j.Conclusion, SkipReason: j.SkipReason, SkipDetail: j.SkipDetail, RunnerName: j.RunnerName,
		OnAirship: j.RunnerID != nil, QueuedAt: j.QueuedAt, StartedAt: j.StartedAt, CompletedAt: j.CompletedAt,
	}
}

// JobList is a page of jobs, newest first.
type JobList struct {
	Items      []Job   `json:"items"`
	NextCursor *string `doc:"Pass as cursor to get the next page" json:"nextCursor"`
}

type listJobsInput struct {
	Org    string `maxLength:"100" minLength:"1" path:"org"`
	Status string `doc:"Only jobs with this status" enum:"queued,in_progress,completed" query:"status"`
	Cursor string `doc:"Cursor from a previous page" maxLength:"200" query:"cursor"`
	Limit  int32  `default:"50" doc:"Page size" maximum:"200" minimum:"1" query:"limit"`
}

type jobListOutput struct {
	Body JobList
}

func (h *handlers) listJobs(ctx context.Context, in *listJobsInput) (*jobListOutput, error) {
	inst, err := h.org(ctx, in.Org, false)
	if err != nil {
		return nil, err
	}
	params := db.ListJobsParams{InstallationID: inst.ID, MaxResults: in.Limit + 1}
	if in.Status != "" {
		params.Status = &in.Status
	}
	if in.Cursor != "" {
		at, id, err := decodeCursor(in.Cursor)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity("invalid cursor")
		}
		jobID, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity("invalid cursor")
		}
		params.CursorQueuedAt, params.CursorID = &at, &jobID
	}
	rows, err := h.Store.ListJobs(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	out := JobList{Items: make([]Job, 0, len(rows))}
	for i, row := range rows {
		if i == int(in.Limit) {
			cursor := encodeCursor(rows[i-1].QueuedAt, strconv.FormatInt(rows[i-1].ID, 10))
			out.NextCursor = &cursor

			break
		}
		out.Items = append(out.Items, jobFromDB(row))
	}

	return &jobListOutput{Body: out}, nil
}

// Runner is one ephemeral runner Airship created.
type Runner struct {
	ID           uuid.UUID  `doc:"Runner ID"                                    format:"uuid" json:"id"`
	Name         string     `doc:"Name registered with GitHub"                  json:"name"`
	State        string     `doc:"Lifecycle state"                              enum:"pending,launching,running,busy,finished" json:"state"`
	FinishReason *string    `doc:"Why the runner finished"                      json:"finishReason"`
	Error        *string    `doc:"Launch error from the cloud or GitHub"        json:"error"`
	Repository   string     `doc:"Repository the runner is registered to"       json:"repository"`
	Definition   string     `doc:"Runner name from the config file"             json:"definition"`
	Source       string     `doc:"Which file defined the runner"                enum:"repository,organization" json:"source"`
	Cloud        string     `doc:"Cloud it launched in"                         enum:"aws,gcp,azure,daytona,runloop,e2b,novita,lelantos,namespace,tenki,modal" json:"cloud"`
	Region       string     `doc:"Region it launched in"                        json:"region"`
	InstanceType string     `doc:"Machine type it launched as"                  json:"instanceType"`
	Spot         bool       `doc:"Whether it uses spot capacity"                json:"spot"`
	Labels       []string   `doc:"Labels it registered with"                    json:"labels"`
	Warm         bool       `doc:"Whether it was started ahead of demand by an idle pool" json:"warm"`
	InstanceID   *string    `doc:"Cloud instance ID"                            json:"instanceId"`
	JobID        *int64     `doc:"Job it was created for"                       json:"jobId"`
	CreatedAt    time.Time  `doc:"When demand for it was created"               json:"createdAt"`
	RunningAt    *time.Time `doc:"When its instance was launched"               json:"runningAt"`
	BusyAt       *time.Time `doc:"When it started a job"                        json:"busyAt"`
	FinishedAt   *time.Time `doc:"When it finished"                             json:"finishedAt"`
}

// RunnerList is a page of runners, newest first.
type RunnerList struct {
	Items      []Runner `json:"items"`
	NextCursor *string  `doc:"Pass as cursor to get the next page" json:"nextCursor"`
}

type listRunnersInput struct {
	Org    string `maxLength:"100" minLength:"1" path:"org"`
	Active bool   `doc:"Only runners that have not finished" query:"active"`
	Cursor string `doc:"Cursor from a previous page" maxLength:"200" query:"cursor"`
	Limit  int32  `default:"50" doc:"Page size" maximum:"200" minimum:"1" query:"limit"`
}

type runnerListOutput struct {
	Body RunnerList
}

func (h *handlers) listRunners(ctx context.Context, in *listRunnersInput) (*runnerListOutput, error) {
	inst, err := h.org(ctx, in.Org, false)
	if err != nil {
		return nil, err
	}
	params := db.ListRunnersParams{InstallationID: inst.ID, ActiveOnly: in.Active, MaxResults: in.Limit + 1}
	if in.Cursor != "" {
		at, id, err := decodeCursor(in.Cursor)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity("invalid cursor")
		}
		runnerID, err := uuid.Parse(id)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity("invalid cursor")
		}
		params.CursorCreatedAt, params.CursorID = &at, &runnerID
	}
	rows, err := h.Store.ListRunners(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("list runners: %w", err)
	}
	out := RunnerList{Items: make([]Runner, 0, len(rows))}
	for i, row := range rows {
		if i == int(in.Limit) {
			cursor := encodeCursor(rows[i-1].CreatedAt, rows[i-1].ID.String())
			out.NextCursor = &cursor

			break
		}
		var spec struct {
			InstanceType string `json:"instanceType"`
			Spot         bool   `json:"spot"`
		}
		_ = json.Unmarshal(row.Spec, &spec) // written by the fleet; a bad row just shows no type
		out.Items = append(out.Items, Runner{
			ID: row.ID, Name: row.Name, State: row.State, FinishReason: row.FinishReason, Error: row.Error,
			Repository: row.RepositoryFullName, Definition: row.Definition, Source: row.Source,
			Cloud: row.Cloud, Region: row.Region, InstanceType: spec.InstanceType, Spot: spec.Spot,
			Labels: row.Labels, Warm: row.PoolID != nil, InstanceID: row.InstanceID, JobID: row.TriggerJobID,
			CreatedAt: row.CreatedAt, RunningAt: row.RunningAt, BusyAt: row.BusyAt, FinishedAt: row.FinishedAt,
		})
	}

	return &runnerListOutput{Body: out}, nil
}

// Event is something that happened in the organization.
type Event struct {
	ID         uuid.UUID  `doc:"Event ID"                                   format:"uuid" json:"id"`
	Kind       string     `doc:"What kind of event, such as runner.launched" json:"kind"`
	Level      string     `doc:"Severity"                                   enum:"info,warning,error" json:"level"`
	Message    string     `doc:"What happened"                              json:"message"`
	Repository *string    `doc:"Repository it concerns"                     json:"repository"`
	RunnerID   *uuid.UUID `doc:"Runner it concerns"                         format:"uuid" json:"runnerId"`
	JobID      *int64     `doc:"Job it concerns"                            json:"jobId"`
	CreatedAt  time.Time  `doc:"When it happened"                           json:"createdAt"`
}

// EventList is a page of events, newest first.
type EventList struct {
	Items      []Event `json:"items"`
	NextCursor *string `doc:"Pass as cursor to get the next page" json:"nextCursor"`
}

type listEventsInput struct {
	Org    string `maxLength:"100" minLength:"1" path:"org"`
	Level  string `doc:"Only events of this level" enum:"info,warning,error" query:"level"`
	Cursor string `doc:"Cursor from a previous page" maxLength:"200" query:"cursor"`
	Limit  int32  `default:"50" doc:"Page size" maximum:"200" minimum:"1" query:"limit"`
}

type eventListOutput struct {
	Body EventList
}

func (h *handlers) listEvents(ctx context.Context, in *listEventsInput) (*eventListOutput, error) {
	inst, err := h.org(ctx, in.Org, false)
	if err != nil {
		return nil, err
	}
	params := db.ListEventsParams{InstallationID: inst.ID, MaxResults: in.Limit + 1}
	if in.Level != "" {
		params.Level = &in.Level
	}
	if in.Cursor != "" {
		at, id, err := decodeCursor(in.Cursor)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity("invalid cursor")
		}
		eventID, err := uuid.Parse(id)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity("invalid cursor")
		}
		params.CursorCreatedAt, params.CursorID = &at, &eventID
	}
	rows, err := h.Store.ListEvents(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	out := EventList{Items: make([]Event, 0, len(rows))}
	for i, row := range rows {
		if i == int(in.Limit) {
			cursor := encodeCursor(rows[i-1].CreatedAt, rows[i-1].ID.String())
			out.NextCursor = &cursor

			break
		}
		out.Items = append(out.Items, Event{
			ID: row.ID, Kind: row.Kind, Level: row.Level, Message: row.Message, Repository: row.Repository,
			RunnerID: row.RunnerID, JobID: row.JobID, CreatedAt: row.CreatedAt,
		})
	}

	return &eventListOutput{Body: out}, nil
}

// DailyUsage is one day of the analytics series.
type DailyUsage struct {
	Day             time.Time `doc:"Start of the day (UTC)"                   json:"day"`
	Jobs            int64     `doc:"Jobs queued"                               json:"jobs"`
	AirshipJobs     int64     `doc:"Of those, jobs that ran on Airship"        json:"airshipJobs"`
	Failed          int64     `doc:"Jobs that failed"                          json:"failed"`
	RunnerMinutes   float64   `doc:"Minutes of jobs run on Airship"            json:"runnerMinutes"`
	QueueP50Seconds float64   `doc:"Median wait from queued to started"        json:"queueP50Seconds"`
}

// RepositoryUsage is one repository's totals over the analytics window.
type RepositoryUsage struct {
	Repository    string  `doc:"Repository (owner/name)"            json:"repository"`
	Jobs          int64   `doc:"Jobs queued"                        json:"jobs"`
	AirshipJobs   int64   `doc:"Jobs that ran on Airship"           json:"airshipJobs"`
	RunnerMinutes float64 `doc:"Minutes of jobs run on Airship"     json:"runnerMinutes"`
}

// Analytics is usage over a window of days.
type Analytics struct {
	Days         int               `doc:"Window length in days"            json:"days"`
	Daily        []DailyUsage      `doc:"Per-day series, oldest first"     json:"daily"`
	Repositories []RepositoryUsage `doc:"Busiest repositories"             json:"repositories"`
}

type analyticsInput struct {
	Org  string `maxLength:"100" minLength:"1" path:"org"`
	Days int    `default:"30" doc:"Window length in days" maximum:"365" minimum:"1" query:"days"`
}

type analyticsOutput struct {
	Body Analytics
}

func (h *handlers) analytics(ctx context.Context, in *analyticsInput) (*analyticsOutput, error) {
	inst, err := h.org(ctx, in.Org, false)
	if err != nil {
		return nil, err
	}
	since := time.Now().UTC().AddDate(0, 0, -in.Days).Truncate(24 * time.Hour)
	daily, err := h.Store.JobDailyStats(ctx, db.JobDailyStatsParams{InstallationID: inst.ID, Since: since})
	if err != nil {
		return nil, fmt.Errorf("daily stats: %w", err)
	}
	repos, err := h.Store.JobRepositoryStats(ctx, db.JobRepositoryStatsParams{InstallationID: inst.ID, Since: since, MaxResults: 20})
	if err != nil {
		return nil, fmt.Errorf("repository stats: %w", err)
	}
	out := Analytics{Days: in.Days, Daily: make([]DailyUsage, 0, len(daily)), Repositories: make([]RepositoryUsage, 0, len(repos))}
	for _, d := range daily {
		out.Daily = append(out.Daily, DailyUsage{Day: d.Day, Jobs: d.Jobs, AirshipJobs: d.AirshipJobs, Failed: d.Failed, RunnerMinutes: d.RunnerSeconds / 60, QueueP50Seconds: d.QueueP50Seconds})
	}
	for _, r := range repos {
		out.Repositories = append(out.Repositories, RepositoryUsage{Repository: r.RepositoryFullName, Jobs: r.Jobs, AirshipJobs: r.AirshipJobs, RunnerMinutes: r.RunnerSeconds / 60})
	}

	return &analyticsOutput{Body: out}, nil
}

// Cursors are opaque to clients: base64 of "<unix nanos>|<id>".
func encodeCursor(at time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(at.UnixNano(), 10) + "|" + id))
}

func decodeCursor(cursor string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("decode cursor: %w", err)
	}
	nanos, id, ok := strings.Cut(string(raw), "|")
	if !ok {
		return time.Time{}, "", errors.New("malformed cursor")
	}
	n, err := strconv.ParseInt(nanos, 10, 64)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("decode cursor time: %w", err)
	}

	return time.Unix(0, n), id, nil
}
