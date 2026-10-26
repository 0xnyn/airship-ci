// Code generated from apps/api/openapi.json by scripts/generate.ts. DO NOT EDIT.
// Change the Go handlers in apps/api, then run `pnpm generate`.

export interface paths {
    "/api/v1/repo": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List repositories (Cirun-compatible) */
        get: operations["cirunListRepositories"];
        put?: never;
        /** Activate or deactivate a repository (Cirun-compatible) */
        post: operations["cirunSetRepository"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/healthz": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Check service health */
        get: operations["getHealth"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/agent/sync": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Sync a data plane agent
         * @description Reports the agent's inventory and returns runners to launch and instances to terminate. Long-polls up to waitSeconds when there is nothing to do.
         */
        post: operations["agentSync"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/api/repositories": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List repositories */
        get: operations["apiListRepositories"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/api/repositories/{name}/activation": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        /** Activate a repository */
        put: operations["apiActivateRepository"];
        post?: never;
        /** Deactivate a repository */
        delete: operations["apiDeactivateRepository"];
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/auth/config": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Get sign-in options */
        get: operations["getAuthConfig"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/auth/dev-sign-in": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Sign in without GitHub (development only) */
        post: operations["devSignIn"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/auth/sign-out": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Sign out */
        post: operations["signOut"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/me": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Get the signed-in user
         * @description Returns the user and the GitHub organizations they can manage in Airship.
         */
        get: operations["getMe"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/orgs/{org}/api-tokens": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List developer API tokens */
        get: operations["listApiTokens"];
        put?: never;
        /** Create a developer API token */
        post: operations["createApiToken"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/orgs/{org}/api-tokens/{id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post?: never;
        /** Revoke a developer API token */
        delete: operations["deleteApiToken"];
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/orgs/{org}/data-planes": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List data planes */
        get: operations["listDataPlanes"];
        put?: never;
        /** Create a data plane */
        post: operations["createDataPlane"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/orgs/{org}/data-planes/{id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post?: never;
        /** Delete a data plane */
        delete: operations["deleteDataPlane"];
        options?: never;
        head?: never;
        /** Update a data plane */
        patch: operations["updateDataPlane"];
        trace?: never;
    };
    "/v1/orgs/{org}/data-planes/{id}/rotate-token": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Rotate a data plane's agent token */
        post: operations["rotateDataPlaneToken"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/orgs/{org}/jobs": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List jobs */
        get: operations["listJobs"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/orgs/{org}/overview": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Get an organization's overview */
        get: operations["getOverview"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/orgs/{org}/pools": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List warm pools */
        get: operations["listPools"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/orgs/{org}/profiles": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List runner profiles */
        get: operations["listProfiles"];
        put?: never;
        /** Create a runner profile */
        post: operations["createProfile"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/orgs/{org}/profiles/{id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post?: never;
        /** Delete a runner profile */
        delete: operations["deleteProfile"];
        options?: never;
        head?: never;
        /** Update a runner profile */
        patch: operations["updateProfile"];
        trace?: never;
    };
    "/v1/orgs/{org}/repositories": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List repositories and their runner config */
        get: operations["listRepositories"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/orgs/{org}/repositories/{repo}/activation": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        /** Activate a repository's runner config */
        put: operations["activateRepository"];
        post?: never;
        /** Deactivate a repository's runner config */
        delete: operations["deactivateRepository"];
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/orgs/{org}/runner-config": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Get the organization's runner config */
        get: operations["getOrganizationConfig"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/orgs/{org}/runner-config/validate": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Check a runner config file */
        post: operations["validateRunnerConfig"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/orgs/{org}/runners": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List runners */
        get: operations["listRunners"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
}
export type webhooks = Record<string, never>;
export interface components {
    schemas: {
        APIToken: {
            /**
             * Format: date-time
             * @description When it was created
             */
            createdAt: string;
            /**
             * Format: uuid
             * @description Token ID
             */
            id: string;
            /**
             * Format: date-time
             * @description When it was last used, to the minute
             */
            lastUsedAt: string | null;
            /** @description What the token is for */
            name: string;
            /** @description First characters of the token */
            tokenHint: string;
        };
        APITokenList: {
            items: components["schemas"]["APIToken"][] | null;
        };
        APITokenSecret: {
            apiToken: components["schemas"]["APIToken"];
            /** @description The token. Store it now: it cannot be shown again. */
            token: string;
        };
        ActivateRepository: {
            /**
             * @description Serve jobs while the repository is public. Public repositories run code from fork pull requests on your machines.
             * @default false
             */
            allowPublic: boolean;
        };
        AuthConfig: {
            /** @description Whether development sign-in is enabled */
            devSignIn: boolean;
            /** @description Where to send the browser to sign in with GitHub */
            githubSignInUrl: string | null;
            /** @description Where to install the GitHub App */
            installUrl: string | null;
        };
        ConfigProblem: {
            /** @description What is wrong */
            detail: string;
            /** @description Where in the file, such as runners[0].instance_type */
            path: string;
        };
        ConfigRunner: {
            /**
             * @description Where it launches
             * @enum {string}
             */
            cloud: "aws" | "on_prem";
            /** @description Data plane it is pinned to, if any */
            dataPlane?: string;
            /** @description Warm pool summary, if it has one */
            idle?: string;
            /** @description runs-on labels it serves */
            labels: string[] | null;
            /** @description Machine shapes to try, in fallback order */
            machines: string[] | null;
            /** @description Runner name */
            name: string;
        };
        ConfigState: {
            /**
             * Format: date-time
             * @description When it was last read
             */
            checkedAt: string | null;
            /** @description Why the file cannot be used */
            error: string | null;
            /** @description Blob SHA of the file; null when there is none */
            sha: string | null;
        };
        ConfigValidation: {
            /** @description The first problem found */
            problem: components["schemas"]["ConfigProblem"];
            /** @description Runner definitions in the file */
            runners: components["schemas"]["ConfigRunner"][] | null;
            /** @description Whether the file can be used */
            valid: boolean;
        };
        Cookie: {
            Domain: string;
            /** Format: date-time */
            Expires: string;
            HttpOnly: boolean;
            /** Format: int64 */
            MaxAge: number;
            Name: string;
            Partitioned: boolean;
            Path: string;
            Quoted: boolean;
            Raw: string;
            RawExpires: string;
            /** Format: int64 */
            SameSite: number;
            Secure: boolean;
            Unparsed: string[] | null;
            Value: string;
        };
        CreateAPIToken: {
            /** @description What the token is for */
            name: string;
        };
        CreateDataPlane: {
            /** @description Name, unique in the organization */
            name: string;
        };
        CreateProfile: {
            /**
             * @description Run jobs from public repositories, including fork pull requests
             * @default false
             */
            allowPublicRepos: boolean;
            /**
             * @description CPU architecture
             * @enum {string}
             */
            arch: "x64" | "arm64";
            /**
             * Format: uuid
             * @description Data plane that launches the runners
             */
            dataPlaneId: string;
            /**
             * Format: int32
             * @description Root disk size in GiB
             * @default 75
             */
            diskGb: number;
            /** @description Machine image; omit to use the agent's default */
            image?: string;
            /** @description Machine type, such as c7i.xlarge */
            instanceType: string;
            /** @description runs-on label; lowercase letters, digits, '.', '_' and '-' */
            label: string;
            /**
             * Format: int32
             * @description Most runners of this profile at once
             * @default 20
             */
            maxRunners: number;
            /**
             * @description Use spot capacity
             * @default false
             */
            spot: boolean;
        };
        DataPlane: {
            /** @description Agent build last seen */
            agentVersion: string | null;
            /**
             * Format: date-time
             * @description When the data plane was created
             */
            createdAt: string;
            /**
             * Format: uuid
             * @description Data plane ID
             */
            id: string;
            /**
             * Format: date-time
             * @description When the agent last synced
             */
            lastSeenAt: string | null;
            /**
             * Format: int32
             * @description Most active runners on this data plane at once
             */
            maxRunners: number;
            /** @description Name, unique in the organization */
            name: string;
            /** @description Where the agent launches runners */
            provider: string | null;
            /** @description Region the agent reported, for runner configs */
            region: string | null;
            /**
             * @description Whether the agent is connected
             * @enum {string}
             */
            status: "online" | "offline" | "pending";
            /** @description First characters of the agent token */
            tokenHint: string;
        };
        DataPlaneCredentials: {
            /** @description URL the agent connects to */
            controlPlaneUrl: string;
            dataPlane: components["schemas"]["DataPlane"];
            /** @description Agent token. Store it now: it cannot be shown again. */
            token: string;
        };
        DataPlaneList: {
            items: components["schemas"]["DataPlane"][] | null;
        };
        ErrorDetail: {
            /** @description Where the error occurred, e.g. 'body.items[3].tags' or 'path.thing-id' */
            location?: string;
            /** @description Error message text */
            message?: string;
            /** @description The value at the given location */
            value?: unknown;
        };
        ErrorModel: {
            /**
             * @description A human-readable explanation specific to this occurrence of the problem.
             * @example Property foo is required but is missing.
             */
            detail?: string;
            /** @description Optional list of individual error details */
            errors?: components["schemas"]["ErrorDetail"][] | null;
            /**
             * Format: uri
             * @description A URI reference that identifies the specific occurrence of the problem.
             * @example https://example.com/error-log/abc123
             */
            instance?: string;
            /**
             * Format: int64
             * @description HTTP status code
             * @example 400
             */
            status?: number;
            /**
             * @description A short, human-readable summary of the problem type. This value should not change between occurrences of the error.
             * @example Bad Request
             */
            title?: string;
            /**
             * Format: uri
             * @description A URI reference to human-readable documentation for the error.
             * @default about:blank
             * @example https://example.com/errors/example
             */
            type: string;
        };
        HealthStatus: {
            /**
             * @description Database status
             * @enum {string}
             */
            database: "ok" | "down";
            /**
             * @description Overall status
             * @enum {string}
             */
            status: "ok" | "degraded";
            /** @description Running api build */
            version: string;
        };
        Instance: {
            /** @description Provider's instance ID */
            instanceId: string;
            /**
             * Format: uuid
             * @description Airship runner ID the instance was launched for
             */
            runnerId: string;
            /** @description The instance is already shutting down */
            stopping?: boolean;
        };
        Job: {
            /**
             * Format: date-time
             * @description When it completed
             */
            completedAt: string | null;
            /** @description Result once completed */
            conclusion: string | null;
            /** @description Branch the job ran for */
            headBranch: string | null;
            /** @description Link to the job on GitHub */
            htmlUrl: string | null;
            /**
             * Format: int64
             * @description GitHub job ID
             */
            id: number;
            /** @description runs-on labels */
            labels: string[] | null;
            /** @description Job name */
            name: string;
            /** @description Whether the job ran on an Airship runner */
            onAirship: boolean;
            /**
             * Format: uuid
             * @description Profile that served the job, if any
             */
            profileId: string;
            /**
             * Format: date-time
             * @description When the job was queued
             */
            queuedAt: string;
            /** @description Repository (owner/name) */
            repository: string;
            /** @description Runner GitHub ran the job on */
            runnerName: string | null;
            /** @description The skip explained, for config file runners */
            skipDetail: string | null;
            /**
             * @description Why Airship did not run a job that asked for it
             * @enum {string|null}
             */
            skipReason: "public_repository" | "unsatisfiable_labels" | "ambiguous_labels" | "installation_suspended" | "repository_inactive" | "config_invalid" | "access_denied" | "no_data_plane" | null;
            /**
             * Format: date-time
             * @description When it started
             */
            startedAt: string | null;
            /**
             * @description Job status
             * @enum {string}
             */
            status: "queued" | "in_progress" | "completed";
            /** @description Workflow name */
            workflowName: string | null;
        };
        JobList: {
            items: components["schemas"]["Job"][] | null;
            /** @description Pass as cursor to get the next page */
            nextCursor: string | null;
        };
        Launch: {
            /** @description Single-use GitHub runner registration. Treat as a secret. */
            jitConfig: string;
            /**
             * Format: int64
             * @description Hard deadline after which the instance powers itself off
             */
            maxLifetimeSeconds: number;
            profile: components["schemas"]["LaunchProfile"];
            /** @description Repository the runner is registered to (owner/name) */
            repository: string;
            /**
             * Format: uuid
             * @description Airship runner ID; tag the instance with it
             */
            runnerId: string;
            /** @description GitHub runner name */
            runnerName: string;
            /** @description actions/runner version to run */
            runnerVersion: string;
        };
        LaunchFailure: {
            /** @description Provider error, shown to the customer */
            error: string;
            /** @description Whether launching again could succeed */
            retryable: boolean;
            /**
             * Format: uuid
             * @description Runner the launch was for
             */
            runnerId: string;
        };
        LaunchProfile: {
            /**
             * @description CPU architecture
             * @enum {string}
             */
            arch: "x64" | "arm64";
            /**
             * Format: int64
             * @description Root disk size in GiB
             */
            diskGb: number;
            /**
             * Format: int64
             * @description Provisioned IOPS; 0 means the volume type's default
             */
            diskIops?: number;
            /**
             * Format: int64
             * @description gp3 throughput in MiB/s; 0 means the default
             */
            diskThroughput?: number;
            /** @description Root volume type (gp3, gp2, io1, io2); empty means gp3 */
            diskType?: string;
            /** @description Environment variables for the runner's jobs */
            env?: {
                [key: string]: string;
            };
            /** @description Machine image; empty means the agent's default for the arch */
            image?: string;
            /** @description Provider machine type, such as c7i.xlarge */
            instanceType: string;
            /** @description runs-on label */
            label: string;
            /** @description Run the container privileged (Docker provider) */
            privileged?: boolean;
            /** @description Use spot or preemptible capacity */
            spot: boolean;
        };
        Me: {
            /** @description Avatar image URL */
            avatarUrl: string | null;
            /** @description GitHub login */
            login: string;
            /** @description Display name */
            name: string | null;
            /** @description Organizations with Airship installed */
            organizations: components["schemas"]["Organization"][] | null;
        };
        Organization: {
            /**
             * Format: int64
             * @description GitHub App installation ID
             */
            installationId: number;
            /** @description GitHub account login */
            login: string;
            /**
             * @description The user's role; only admins change settings
             * @enum {string}
             */
            role: "admin" | "member";
            /** @description Whether the installation is suspended */
            suspended: boolean;
            /**
             * @description GitHub account type
             * @enum {string}
             */
            type: "Organization" | "User";
        };
        OrganizationRunnerConfig: {
            /** @description Whether the organization has a .airship.global.yml */
            found: boolean;
            /** @description Why the files cannot be used */
            problem: string | null;
            /** @description Whether .access.yml limits who can use the runners */
            restricted: boolean;
            /** @description Organization runner definitions */
            runners: components["schemas"]["ConfigRunner"][] | null;
            /** @description Blob SHA of .airship.global.yml */
            sha: string | null;
        };
        Overview: {
            /**
             * Format: int64
             * @description Of those, jobs that ran on Airship
             */
            airshipJobs: number;
            /**
             * Format: int64
             * @description Jobs that failed
             */
            failed: number;
            /**
             * Format: int64
             * @description Jobs running now
             */
            inProgress: number;
            /**
             * Format: int64
             * @description Jobs queued in the window
             */
            jobs: number;
            /**
             * Format: double
             * @description Median wait from queued to started on Airship
             */
            queueP50Seconds: number;
            /**
             * Format: double
             * @description 90th percentile wait from queued to started
             */
            queueP90Seconds: number;
            /**
             * Format: int64
             * @description Jobs still waiting
             */
            queued: number;
            /**
             * Format: double
             * @description Minutes of jobs run on Airship
             */
            runnerMinutes: number;
            runners: components["schemas"]["RunnerCounts"];
            /**
             * Format: int64
             * @description Jobs that succeeded
             */
            succeeded: number;
        };
        Pool: {
            /**
             * Format: int64
             * @description Idle runners the pool wants now
             */
            desired: number;
            /**
             * Format: uuid
             * @description Pool ID
             */
            id: string;
            /**
             * Format: int32
             * @description Runners waiting for a job
             */
            idle: number;
            /**
             * @description How the pool's size is decided
             * @enum {string}
             */
            mode: "fixed" | "schedule" | "usage";
            /** @description Runner definition name */
            name: string;
            /** @description Repository whose .airship.yml defines it */
            repository: string;
            /**
             * Format: int32
             * @description Pool runners that took a job and are not done
             */
            taken: number;
            /**
             * Format: date-time
             * @description When the definition last changed
             */
            updatedAt: string;
            /**
             * Format: date-time
             * @description Usage-based pools stay warm until this time
             */
            warmUntil: string | null;
        };
        PoolList: {
            items: components["schemas"]["Pool"][] | null;
        };
        Profile: {
            /** @description Run jobs from public repositories, including fork pull requests */
            allowPublicRepos: boolean;
            /**
             * @description CPU architecture
             * @enum {string}
             */
            arch: "x64" | "arm64";
            /**
             * Format: date-time
             * @description When the profile was created
             */
            createdAt: string;
            /**
             * Format: uuid
             * @description Data plane that launches the runners
             */
            dataPlaneId: string;
            /**
             * Format: int32
             * @description Root disk size in GiB
             */
            diskGb: number;
            /**
             * Format: uuid
             * @description Profile ID
             */
            id: string;
            /** @description Machine image; null uses the agent's default */
            image: string | null;
            /** @description Machine type, such as c7i.xlarge */
            instanceType: string;
            /** @description The runs-on label that selects this profile */
            label: string;
            /**
             * Format: int32
             * @description Most runners of this profile at once
             */
            maxRunners: number;
            /** @description Use spot capacity */
            spot: boolean;
            /**
             * Format: date-time
             * @description When the profile last changed
             */
            updatedAt: string;
        };
        ProfileList: {
            items: components["schemas"]["Profile"][] | null;
        };
        Repository: {
            /** @description Whether the app can still access the repository */
            accessible: boolean;
            /** @description Whether its .airship.yml launches runners */
            activated: boolean;
            /**
             * Format: date-time
             * @description When it was activated
             */
            activatedAt: string | null;
            /** @description Serve jobs while the repository is public */
            allowPublic: boolean;
            /** @description The default branch's .airship.yml as last read */
            config: components["schemas"]["ConfigState"];
            /** @description Branch idle runner config is read from */
            defaultBranch: string;
            /** @description owner/name */
            fullName: string;
            /**
             * Format: int64
             * @description GitHub repository ID
             */
            id: number;
            /** @description Whether the repository is private */
            private: boolean;
        };
        RepositoryList: {
            items: components["schemas"]["Repository"][] | null;
        };
        Runner: {
            /**
             * Format: date-time
             * @description When it started a job
             */
            busyAt: string | null;
            /**
             * Format: date-time
             * @description When demand for it was created
             */
            createdAt: string;
            /**
             * Format: uuid
             * @description Data plane that runs it
             */
            dataPlaneId: string;
            /** @description Launch error reported by the data plane */
            error: string | null;
            /** @description Why the runner finished */
            finishReason: string | null;
            /**
             * Format: date-time
             * @description When it finished
             */
            finishedAt: string | null;
            /**
             * Format: uuid
             * @description Runner ID
             */
            id: string;
            /** @description Provider instance ID */
            instanceId: string | null;
            /** @description Machine type it launched as */
            instanceType: string;
            /** @description Labels it registered with */
            labels: string[] | null;
            /** @description Name registered with GitHub */
            name: string;
            /** @description Profile label, or config runner name */
            profileLabel: string;
            /** @description Repository the runner is registered to */
            repository: string;
            /**
             * Format: date-time
             * @description When its instance was first seen
             */
            runningAt: string | null;
            /**
             * @description What defined the runner
             * @enum {string}
             */
            source: "profile" | "repository" | "organization";
            /**
             * @description Lifecycle state
             * @enum {string}
             */
            state: "pending" | "assigned" | "running" | "busy" | "finished";
            /** @description Whether it was started ahead of demand by a warm pool */
            warm: boolean;
        };
        RunnerCounts: {
            /**
             * Format: int64
             * @description Running a job
             */
            busy: number;
            /**
             * Format: int64
             * @description Online, waiting for a job
             */
            idle: number;
            /**
             * Format: int64
             * @description Waiting for capacity
             */
            pending: number;
            /**
             * Format: int64
             * @description Handed to an agent or booting
             */
            starting: number;
        };
        RunnerList: {
            items: components["schemas"]["Runner"][] | null;
            /** @description Pass as cursor to get the next page */
            nextCursor: string | null;
        };
        SetRepository: {
            /** @description true activates, false deactivates */
            active: boolean;
            /** @description owner/name, or just the name */
            repository: string;
        };
        SyncRequest: {
            /** @description Agent build */
            agentVersion: string;
            /**
             * Format: int64
             * @description Launches the agent will accept in this response
             */
            capacity: number;
            /** @description Launches that failed since the last sync */
            failures: components["schemas"]["LaunchFailure"][] | null;
            /** @description Every live runner instance the provider reports for this data plane */
            instances: components["schemas"]["Instance"][] | null;
            /** @description Instances is not a complete inventory; skip reconciliation */
            inventoryUnknown?: boolean;
            /**
             * @description Provider the agent launches runners with
             * @enum {string}
             */
            provider: "aws" | "docker" | "fake";
            /** @description Runner IDs received in launches recently */
            received?: string[] | null;
            /** @description Region the provider launches in */
            region?: string;
            /**
             * Format: int64
             * @description Seconds to hold the request open when there is no work
             */
            waitSeconds: number;
        };
        SyncResponse: {
            /**
             * Format: uuid
             * @description Data plane the token belongs to
             */
            dataPlaneId: string;
            /** @description Runners to launch */
            launch: components["schemas"]["Launch"][] | null;
            /** @description Instances to terminate */
            terminate: components["schemas"]["Terminate"][] | null;
        };
        Terminate: {
            /** @description Provider's instance ID */
            instanceId: string;
            /** @description Why the instance should go */
            reason: string;
            /**
             * Format: uuid
             * @description Runner the instance belongs to
             */
            runnerId: string;
        };
        UpdateDataPlane: {
            /**
             * Format: int32
             * @description Most active runners on this data plane at once, across profiles and config files
             */
            maxRunners: number;
        };
        UpdateProfile: {
            /** @description Run jobs from public repositories */
            allowPublicRepos?: boolean;
            /**
             * Format: int32
             * @description New root disk size in GiB
             */
            diskGb?: number;
            /** @description New machine image; an empty string restores the agent's default */
            image?: string;
            /** @description New machine type */
            instanceType?: string;
            /**
             * Format: int32
             * @description Most runners at once
             */
            maxRunners?: number;
            /** @description Use spot capacity */
            spot?: boolean;
        };
        ValidateConfig: {
            /** @description The file's content */
            content: string;
            /**
             * @description Which file this is
             * @enum {string}
             */
            file: ".airship.yml" | ".airship.global.yml" | ".access.yml";
        };
    };
    responses: never;
    parameters: never;
    requestBodies: never;
    headers: never;
    pathItems: never;
}
export type $defs = Record<string, never>;
export interface operations {
    cirunListRepositories: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["RepositoryList"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    cirunSetRepository: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["SetRepository"];
            };
        };
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Repository"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    getHealth: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["HealthStatus"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    agentSync: {
        parameters: {
            query?: never;
            header: {
                /** @description Bearer data plane token */
                Authorization: string;
            };
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["SyncRequest"];
            };
        };
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["SyncResponse"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    apiListRepositories: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["RepositoryList"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    apiActivateRepository: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description Repository name, without the owner */
                name: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["ActivateRepository"];
            };
        };
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Repository"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    apiDeactivateRepository: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description Repository name, without the owner */
                name: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description No Content */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    getAuthConfig: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AuthConfig"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    devSignIn: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description No Content */
            204: {
                headers: {
                    "Set-Cookie"?: components["schemas"]["Cookie"];
                    [name: string]: unknown;
                };
                content?: never;
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    signOut: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: {
                airship_session?: string;
            };
        };
        requestBody?: never;
        responses: {
            /** @description No Content */
            204: {
                headers: {
                    "Set-Cookie"?: components["schemas"]["Cookie"];
                    [name: string]: unknown;
                };
                content?: never;
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    getMe: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Me"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    listApiTokens: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description GitHub organization login */
                org: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["APITokenList"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    createApiToken: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                org: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["CreateAPIToken"];
            };
        };
        responses: {
            /** @description Created */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["APITokenSecret"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    deleteApiToken: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description GitHub organization login */
                org: string;
                /** @description Resource ID */
                id: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description No Content */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    listDataPlanes: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description GitHub organization login */
                org: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["DataPlaneList"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    createDataPlane: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                org: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["CreateDataPlane"];
            };
        };
        responses: {
            /** @description Created */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["DataPlaneCredentials"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    deleteDataPlane: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description GitHub organization login */
                org: string;
                /** @description Resource ID */
                id: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description No Content */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    updateDataPlane: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                org: string;
                id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["UpdateDataPlane"];
            };
        };
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["DataPlane"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    rotateDataPlaneToken: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description GitHub organization login */
                org: string;
                /** @description Resource ID */
                id: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["DataPlaneCredentials"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    listJobs: {
        parameters: {
            query?: {
                /** @description Only jobs with this status */
                status?: "queued" | "in_progress" | "completed";
                /** @description Cursor from a previous page */
                cursor?: string;
                /** @description Page size */
                limit?: number;
            };
            header?: never;
            path: {
                org: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["JobList"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    getOverview: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description GitHub organization login */
                org: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Overview"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    listPools: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description GitHub organization login */
                org: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["PoolList"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    listProfiles: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description GitHub organization login */
                org: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ProfileList"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    createProfile: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                org: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["CreateProfile"];
            };
        };
        responses: {
            /** @description Created */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Profile"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    deleteProfile: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description GitHub organization login */
                org: string;
                /** @description Resource ID */
                id: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description No Content */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    updateProfile: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                org: string;
                id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["UpdateProfile"];
            };
        };
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Profile"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    listRepositories: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description GitHub organization login */
                org: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["RepositoryList"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    activateRepository: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                org: string;
                repo: number;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["ActivateRepository"];
            };
        };
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Repository"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    deactivateRepository: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description GitHub organization login */
                org: string;
                /** @description GitHub repository ID */
                repo: number;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description No Content */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    getOrganizationConfig: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description GitHub organization login */
                org: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["OrganizationRunnerConfig"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    validateRunnerConfig: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                org: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["ValidateConfig"];
            };
        };
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ConfigValidation"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    listRunners: {
        parameters: {
            query?: {
                /** @description Only runners that have not finished */
                active?: boolean;
                /** @description Cursor from a previous page */
                cursor?: string;
                /** @description Page size */
                limit?: number;
            };
            header?: never;
            path: {
                org: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["RunnerList"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
}
