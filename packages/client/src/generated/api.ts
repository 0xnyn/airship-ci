// Code generated from apps/api/openapi.json by scripts/generate.ts. DO NOT EDIT.
// Change the Go handlers in apps/api, then run `pnpm generate`.

export interface paths {
    "/api/runner-provision": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Fetch a runner's boot script (provision_via: pull_from_api) */
        get: operations["compatRunnerProvision"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/access-control": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Read the organization's access.yaml */
        get: operations["compatGetAccessControl"];
        /** Open a pull request changing access.yaml */
        put: operations["compatUpdateAccessControl"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/cloud-connect": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List connected clouds */
        get: operations["compatListClouds"];
        put?: never;
        /** Connect a cloud */
        post: operations["compatCloudConnect"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/repo": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List repositories */
        get: operations["compatListRepositories"];
        put?: never;
        /** Activate or deactivate a repository */
        post: operations["compatSetRepository"];
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
    "/v1/assets/cacheproxy/{os}/{arch}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Download the cache proxy binary
         * @description Runner machines with extra_config.cache fetch this at boot.
         */
        get: operations["getCacheProxyBinary"];
        put?: never;
        post?: never;
        delete?: never;
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
    "/v1/orgs/{org}/analytics": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Get usage analytics */
        get: operations["getAnalytics"];
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
    "/v1/orgs/{org}/clouds": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List connected clouds */
        get: operations["listClouds"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/orgs/{org}/clouds/aws": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        /** Connect AWS */
        put: operations["connectAws"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/orgs/{org}/clouds/azure": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        /** Connect Microsoft Azure */
        put: operations["connectAzure"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/orgs/{org}/clouds/daytona": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        /** Connect Daytona */
        put: operations["connectDaytona"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/orgs/{org}/clouds/e2b": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        /** Connect E2B */
        put: operations["connectE2b"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/orgs/{org}/clouds/gcp": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        /** Connect Google Cloud */
        put: operations["connectGcp"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/orgs/{org}/clouds/lelantos": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        /** Connect Lelantos */
        put: operations["connectLelantos"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/orgs/{org}/clouds/modal": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        /** Connect Modal */
        put: operations["connectModal"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/orgs/{org}/clouds/namespace": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        /** Connect Namespace */
        put: operations["connectNamespace"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/orgs/{org}/clouds/novita": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        /** Connect Novita */
        put: operations["connectNovita"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/orgs/{org}/clouds/runloop": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        /** Connect Runloop */
        put: operations["connectRunloop"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/orgs/{org}/clouds/tenki": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        /** Connect Tenki */
        put: operations["connectTenki"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/orgs/{org}/clouds/{cloud}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post?: never;
        /** Disconnect a cloud */
        delete: operations["disconnectCloud"];
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/orgs/{org}/events": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List events */
        get: operations["listEvents"];
        put?: never;
        post?: never;
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
    "/v1/orgs/{org}/members": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List organization members with dashboard access */
        get: operations["listMembers"];
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
        /** List idle runner pools */
        get: operations["listPools"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
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
        /** Activate a repository */
        put: operations["activateRepository"];
        post?: never;
        /** Deactivate a repository */
        delete: operations["deactivateRepository"];
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/orgs/{org}/repositories/{repo}/migration/preview": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Preview the files a migration plan writes */
        post: operations["previewMigration"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/orgs/{org}/repositories/{repo}/migration/pull-request": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Open a pull request that moves workflows onto Airship */
        post: operations["openMigrationPullRequest"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/orgs/{org}/repositories/{repo}/workflows": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Read a repository's workflows for moving them onto Airship */
        get: operations["scanRepositoryWorkflows"];
        put?: never;
        post?: never;
        delete?: never;
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
    "/v1/orgs/{org}/settings": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Get organization settings */
        get: operations["getSettings"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        /** Update organization settings */
        patch: operations["updateSettings"];
        trace?: never;
    };
    "/v1/runner/cache-grant": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Fetch cache bucket credentials for a runner
         * @description Called by the cache proxy on a runner machine with the token its boot script carries. The credentials are scoped to the runner's repository prefix in the installation's bucket and expire; the proxy refreshes them.
         */
        get: operations["runnerCacheGrant"];
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
        AccessControlFile: {
            /** @description The file's content */
            access_yml: components["schemas"]["AccessDocument"];
            /** @description Whether the .airship repository has an access.yaml */
            found: boolean;
        };
        AccessControlUpdate: {
            /** @description The file as proposed */
            access_yml: components["schemas"]["AccessDocument"];
            /** @description Branch the change was pushed to */
            branch: string;
            /** @description Pull request to review and merge */
            pull_request_url: string;
        };
        AccessDocument: {
            access_control: components["schemas"]["AccessRule"][] | null;
            policies: components["schemas"]["PolicyDocument"][] | null;
            /** Format: int64 */
            version: number;
        };
        AccessRule: {
            policies: string[] | null;
            resource: string;
        };
        ActivateRepository: {
            /**
             * @description Serve jobs while the repository is public. Public repositories run code from fork pull requests on your machines.
             * @default false
             */
            allowPublic: boolean;
        };
        Analytics: {
            /** @description Per-day series, oldest first */
            daily: components["schemas"]["DailyUsage"][] | null;
            /**
             * Format: int64
             * @description Window length in days
             */
            days: number;
            /** @description Busiest repositories */
            repositories: components["schemas"]["RepositoryUsage"][] | null;
        };
        AuthConfig: {
            /** @description Whether development sign-in is enabled */
            devSignIn: boolean;
            /** @description Where to send the browser to sign in with GitHub */
            githubSignInUrl: string | null;
            /** @description Where to install the GitHub App */
            installUrl: string | null;
        };
        Cloud: {
            /** @description Cloud account ID (AWS account, GCP project, Azure subscription, Daytona organization) */
            accountId: string | null;
            /**
             * @description Cloud name
             * @enum {string}
             */
            cloud: "aws" | "gcp" | "azure" | "daytona" | "runloop" | "e2b" | "novita" | "lelantos" | "namespace" | "tenki" | "modal";
            /**
             * Format: date-time
             * @description When it was connected
             */
            connectedAt: string;
            /** @description Region (or GCP zone, or a sandbox cloud's region; global on the clouds without regions) used when a config names none */
            defaultRegion: string;
            /** @description Why the credential stopped working */
            error: string | null;
            /** @description Public half of the credential (AWS access key ID, GCP service account email, Azure application ID, a sandbox cloud's masked API key or token ID) */
            identity: string;
            /** @description Who the credential is, such as an IAM ARN or an API key's name */
            principal: string | null;
            /**
             * @description Whether the credential works
             * @enum {string}
             */
            status: "ok" | "invalid";
            /**
             * Format: date-time
             * @description When it was last changed
             */
            updatedAt: string;
            /**
             * Format: date-time
             * @description When it was last verified with the cloud
             */
            verifiedAt: string | null;
        };
        CloudConnect: {
            /**
             * @description Cloud name
             * @enum {string}
             */
            cloud: "aws" | "gcp" | "azure" | "daytona" | "runloop" | "e2b" | "novita" | "lelantos" | "namespace" | "tenki" | "modal";
            /** @description Credentials; for aws: access_key, secret_key and optionally region; for gcp: the service account key JSON's fields and optionally region (a zone or region); for azure: subscription_id, tenant_id, client_id, client_secret and optionally region; for daytona: api_key and optionally api_url and region; for runloop: api_key and optionally api_url; for e2b, novita and lelantos: api_key and optionally domain; for namespace: token and optionally region (us or eu); for tenki: api_key and optionally api_url and workspace_id; for modal: token_id, token_secret and optionally environment */
            credentials: {
                [key: string]: string;
            };
        };
        CloudList: {
            items: components["schemas"]["Cloud"][] | null;
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
            cloud: "aws" | "gcp";
            /** @description Idle pool summary, if it has one */
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
            /** @description The first problem found; absent when valid */
            problem?: components["schemas"]["ConfigProblem"];
            /** @description Runner definitions in the file */
            runners: components["schemas"]["ConfigRunner"][] | null;
            /** @description Whether the file can be used */
            valid: boolean;
        };
        ConnectAWS: {
            /** @description IAM access key ID */
            accessKeyId: string;
            /**
             * @description Region used when a config names none
             * @default eu-west-1
             */
            defaultRegion: string;
            /** @description IAM secret access key */
            secretAccessKey: string;
        };
        ConnectAzure: {
            /**
             * Format: uuid
             * @description Application (client) ID
             */
            clientId: string;
            /** @description A client secret of the application */
            clientSecret: string;
            /**
             * @description Region (eastus, westeurope) used when a config names none
             * @default eastus
             */
            defaultRegion: string;
            /**
             * Format: uuid
             * @description Subscription ID
             */
            subscriptionId: string;
            /**
             * Format: uuid
             * @description Directory (tenant) ID of the application
             */
            tenantId: string;
        };
        ConnectDaytona: {
            /** @description Organization API key (write:sandboxes and delete:sandboxes) */
            apiKey: string;
            /**
             * Format: uri
             * @description API URL, for a self-hosted Daytona; the hosted service by default
             */
            apiUrl?: string;
            /**
             * @description Region used when a config names none
             * @default us
             */
            defaultRegion: string;
        };
        ConnectE2B: {
            /** @description E2B team API key (e2b_...) */
            apiKey: string;
            /** @description Domain of a self-hosted E2B; the hosted service (e2b.app) by default */
            domain?: string;
        };
        ConnectE2BCompatible: {
            /** @description API key */
            apiKey: string;
            /** @description Domain of a self-hosted service; the hosted one by default */
            domain?: string;
        };
        ConnectGCP: {
            /**
             * @description Zone (us-central1-b) or region (us-central1) used when a config names none
             * @default us-central1
             */
            defaultRegion: string;
            /** @description The service account key file's JSON, as downloaded from Google Cloud */
            serviceAccountKey: string;
        };
        ConnectModal: {
            /** @description Environment; the workspace's default when empty */
            environment?: string;
            /** @description Token ID (ak-...) */
            tokenId: string;
            /** @description Token secret (as-...) */
            tokenSecret: string;
        };
        ConnectNamespace: {
            /**
             * @description Region used when a config names none
             * @default us
             * @enum {string}
             */
            defaultRegion: "us" | "eu";
            /** @description Namespace token */
            token: string;
        };
        ConnectRunloop: {
            /** @description Runloop API key */
            apiKey: string;
            /**
             * Format: uri
             * @description API URL; the hosted service by default
             */
            apiUrl?: string;
        };
        ConnectTenki: {
            /** @description Workspace API key (tk_... or lx_...) */
            apiKey: string;
            /**
             * Format: uri
             * @description API URL; the hosted service by default
             */
            apiUrl?: string;
            /** @description Workspace, when the key is not a workspace key */
            workspaceId?: string;
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
        DailyUsage: {
            /**
             * Format: int64
             * @description Of those, jobs that ran on Airship
             */
            airshipJobs: number;
            /**
             * Format: date-time
             * @description Start of the day (UTC)
             */
            day: string;
            /**
             * Format: int64
             * @description Jobs that failed
             */
            failed: number;
            /**
             * Format: int64
             * @description Jobs queued
             */
            jobs: number;
            /**
             * Format: double
             * @description Median wait from queued to started
             */
            queueP50Seconds: number;
            /**
             * Format: double
             * @description Minutes of jobs run on Airship
             */
            runnerMinutes: number;
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
        Event: {
            /**
             * Format: date-time
             * @description When it happened
             */
            createdAt: string;
            /**
             * Format: uuid
             * @description Event ID
             */
            id: string;
            /**
             * Format: int64
             * @description Job it concerns
             */
            jobId: number | null;
            /** @description What kind of event, such as runner.launched */
            kind: string;
            /**
             * @description Severity
             * @enum {string}
             */
            level: "info" | "warning" | "error";
            /** @description What happened */
            message: string;
            /** @description Repository it concerns */
            repository: string | null;
            /**
             * Format: uuid
             * @description Runner it concerns
             */
            runnerId: string;
        };
        EventList: {
            items: components["schemas"]["Event"][] | null;
            /** @description Pass as cursor to get the next page */
            nextCursor: string | null;
        };
        ExistingRunnerConfig: {
            /** @description Why the file cannot be used */
            problem: string | null;
            /** @description Its runner definitions */
            runners: components["schemas"]["ConfigRunner"][];
            /** @description Blob SHA */
            sha: string;
        };
        Grant: {
            accessKeyId: string;
            accountId?: string;
            bucket: string;
            cloud: string;
            /** Format: date-time */
            expiresAt: string;
            prefix: string;
            region: string;
            secretAccessKey: string;
            sessionToken: string;
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
            skipReason: "public_repository" | "unsatisfiable_labels" | "installation_suspended" | "repository_inactive" | "config_invalid" | "access_denied" | "no_cloud" | null;
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
        Member: {
            /** @description Avatar image URL */
            avatarUrl: string | null;
            /** @description GitHub login */
            login: string;
            /** @description Display name */
            name: string | null;
            /**
             * Format: date-time
             * @description When the role was last checked with GitHub
             */
            refreshedAt: string;
            /**
             * @description Dashboard role, from the GitHub organization role
             * @enum {string}
             */
            role: "admin" | "member";
        };
        MemberList: {
            items: components["schemas"]["Member"][] | null;
        };
        MigrationFileChange: {
            /** @description Content after the change */
            after: string;
            /** @description Content today; empty for a new file */
            before: string;
            /** @description Whether the file is new */
            created: boolean;
            /** @description Path in the repository */
            path: string;
        };
        MigrationMove: {
            /** @description Job ID */
            job: string;
            /** @description Label the job runs on, such as airship-linux */
            label: string;
            /** @description Workflow path, such as .github/workflows/ci.yml */
            workflow: string;
        };
        MigrationPlan: {
            /** @description Jobs to move; may be empty when the plan only adds runners */
            moves: components["schemas"]["MigrationMove"][];
            /** @description Definitions to add to airship.yaml */
            runners: components["schemas"]["MigrationRunner"][];
        };
        MigrationPreview: {
            files: components["schemas"]["MigrationFileChange"][];
        };
        MigrationPullRequest: {
            /**
             * Format: int64
             * @description Pull request number
             */
            number: number;
            /**
             * Format: uri
             * @description Where to review it
             */
            url: string;
        };
        MigrationRunner: {
            /**
             * @description Where it launches
             * @enum {string}
             */
            cloud: "aws" | "gcp" | "azure" | "daytona" | "runloop" | "e2b" | "novita" | "lelantos" | "namespace" | "tenki" | "modal";
            /** @description Machine shape, as airship.yaml spells it for the cloud */
            instanceType: string;
            /** @description Image; empty for the cloud's default */
            machineImage?: string;
            /** @description Definition name; its label is airship-<name> */
            name: string;
            /** @description Use spot capacity */
            preemptible?: boolean;
            /** @description Region; empty for the connected cloud's default */
            region?: string;
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
            /** @description Whether the organization has an airship.global.yaml */
            found: boolean;
            /** @description Why the files cannot be used */
            problem: string | null;
            /** @description Whether access.yaml limits who can use the runners */
            restricted: boolean;
            /** @description Organization runner definitions */
            runners: components["schemas"]["ConfigRunner"][] | null;
            /** @description Blob SHA of airship.global.yaml */
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
        PolicyDocument: {
            id: string;
            pull_request?: boolean;
            repo: string;
            roles?: string[] | null;
            teams?: string[] | null;
            users?: string[] | null;
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
            /** @description Repository whose airship.yaml defines it, or the organization */
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
        Repository: {
            /** @description Whether the app can still access the repository */
            accessible: boolean;
            /** @description Whether its airship.yaml launches runners */
            activated: boolean;
            /**
             * Format: date-time
             * @description When it was activated
             */
            activatedAt: string | null;
            /** @description Serve jobs while the repository is public */
            allowPublic: boolean;
            /** @description The default branch's airship.yaml as last read; absent until activated */
            config?: components["schemas"]["ConfigState"];
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
        RepositoryResourceAccess: {
            /**
             * @description add grants, remove revokes
             * @default add
             * @enum {string}
             */
            action: "add" | "remove";
            /** @description Policy options, such as {"pull_request": true} */
            policy_args?: {
                [key: string]: unknown;
            };
            /** @description Repository name, with or without the organization */
            repository: string;
            /** @description Runner names from airship.global.yaml */
            resources: string[] | null;
            /** @description Repository roles allowed */
            roles?: string[] | null;
            /** @description Team slugs allowed */
            teams?: string[] | null;
            /** @description GitHub logins allowed */
            users?: string[] | null;
            /** @description Not supported; refused */
            users_from_json?: string;
        };
        RepositoryUsage: {
            /**
             * Format: int64
             * @description Jobs that ran on Airship
             */
            airshipJobs: number;
            /**
             * Format: int64
             * @description Jobs queued
             */
            jobs: number;
            /** @description Repository (owner/name) */
            repository: string;
            /**
             * Format: double
             * @description Minutes of jobs run on Airship
             */
            runnerMinutes: number;
        };
        Runner: {
            /**
             * Format: date-time
             * @description When it started a job
             */
            busyAt: string | null;
            /**
             * @description Cloud it launched in
             * @enum {string}
             */
            cloud: "aws" | "gcp" | "azure" | "daytona" | "runloop" | "e2b" | "novita" | "lelantos" | "namespace" | "tenki" | "modal";
            /**
             * Format: date-time
             * @description When demand for it was created
             */
            createdAt: string;
            /** @description Runner name from the config file */
            definition: string;
            /** @description Launch error from the cloud or GitHub */
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
            /** @description Cloud instance ID */
            instanceId: string | null;
            /** @description Machine type it launched as */
            instanceType: string;
            /**
             * Format: int64
             * @description Job it was created for
             */
            jobId: number | null;
            /** @description Labels it registered with */
            labels: string[] | null;
            /** @description Name registered with GitHub */
            name: string;
            /** @description Region it launched in */
            region: string;
            /** @description Repository the runner is registered to */
            repository: string;
            /**
             * Format: date-time
             * @description When its instance was launched
             */
            runningAt: string | null;
            /**
             * @description Which file defined the runner
             * @enum {string}
             */
            source: "repository" | "organization";
            /** @description Whether it uses spot capacity */
            spot: boolean;
            /**
             * @description Lifecycle state
             * @enum {string}
             */
            state: "pending" | "launching" | "running" | "busy" | "finished";
            /** @description Whether it was started ahead of demand by an idle pool */
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
             * @description Being launched
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
        Settings: {
            /**
             * Format: int32
             * @description Most live runners at once across every repository
             */
            maxRunners: number;
        };
        SkippedWorkflow: {
            path: string;
            reason: string;
        };
        UpdateAccessControl: {
            /** @description Organization; must match the token's */
            org?: string;
            /** @description Changes to apply */
            repository_resource_access: components["schemas"]["RepositoryResourceAccess"][] | null;
        };
        ValidateConfig: {
            /** @description The file's content */
            content: string;
            /**
             * @description Which file this is
             * @enum {string}
             */
            file: "airship.yaml" | "airship.global.yaml" | "access.yaml";
        };
        WorkflowFile: {
            /** @description Jobs in file order */
            jobs: components["schemas"]["WorkflowJob"][];
            /** @description The workflow's name: field */
            name: string;
            /** @description Path in the repository */
            path: string;
            /** @description Why the file could not be read */
            problem: string | null;
        };
        WorkflowJob: {
            /** @description Whether runs-on can be rewritten automatically */
            editable: boolean;
            /** @description Job ID, the key under jobs: */
            id: string;
            /** @description The closest machine_image alias to the hosted image */
            image: string;
            /**
             * @description What runs-on asks for today
             * @enum {string}
             */
            kind: "hosted" | "airship" | "self-hosted" | "expression" | "group" | "reusable" | "other";
            /**
             * Format: int64
             * @description Line of runs-on in the file
             */
            line: number;
            /** @description Whether the job has a strategy matrix */
            matrix: boolean;
            /** @description The job's name: field, if it has one */
            name: string;
            /**
             * @description The GitHub-hosted machine's platform, when known
             * @enum {string}
             */
            platform: "linux-x64" | "linux-arm64" | "windows" | "macos" | "";
            /** @description Why it cannot be moved automatically */
            reason: string;
            /** @description runs-on labels as written; empty for a runner group or a reusable workflow call */
            runsOn: string[];
        };
        WorkflowScan: {
            /** @description The repository's airship.yaml; absent when there is none */
            config?: components["schemas"]["ExistingRunnerConfig"];
            /** @description Branch the files were read from */
            defaultBranch: string;
            /** @description owner/name */
            repository: string;
            /** @description Labels the organization's airship.global.yaml serves */
            sharedLabels: string[];
            skipped: components["schemas"]["SkippedWorkflow"][];
            workflows: components["schemas"]["WorkflowFile"][];
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
    compatRunnerProvision: {
        parameters: {
            query: {
                /** @description The runner's name, from the instance's Name tag */
                runner_name: string;
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    "Content-Type"?: string;
                    [name: string]: unknown;
                };
                content: {
                    "application/json": string;
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
    compatGetAccessControl: {
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
                    "application/json": components["schemas"]["AccessControlFile"];
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
    compatUpdateAccessControl: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["UpdateAccessControl"];
            };
        };
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AccessControlUpdate"];
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
    compatListClouds: {
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
                    "application/json": components["schemas"]["CloudList"];
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
    compatCloudConnect: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["CloudConnect"];
            };
        };
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Cloud"];
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
    compatListRepositories: {
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
    compatSetRepository: {
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
    getCacheProxyBinary: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description Operating system */
                os: "linux";
                /** @description CPU architecture */
                arch: "x64" | "arm64";
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    "Content-Type"?: string;
                    [name: string]: unknown;
                };
                content: {
                    "application/json": string;
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
    getAnalytics: {
        parameters: {
            query?: {
                /** @description Window length in days */
                days?: number;
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
                    "application/json": components["schemas"]["Analytics"];
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
    listClouds: {
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
                    "application/json": components["schemas"]["CloudList"];
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
    connectAws: {
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
                "application/json": components["schemas"]["ConnectAWS"];
            };
        };
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Cloud"];
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
    connectAzure: {
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
                "application/json": components["schemas"]["ConnectAzure"];
            };
        };
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Cloud"];
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
    connectDaytona: {
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
                "application/json": components["schemas"]["ConnectDaytona"];
            };
        };
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Cloud"];
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
    connectE2b: {
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
                "application/json": components["schemas"]["ConnectE2B"];
            };
        };
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Cloud"];
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
    connectGcp: {
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
                "application/json": components["schemas"]["ConnectGCP"];
            };
        };
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Cloud"];
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
    connectLelantos: {
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
                "application/json": components["schemas"]["ConnectE2BCompatible"];
            };
        };
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Cloud"];
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
    connectModal: {
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
                "application/json": components["schemas"]["ConnectModal"];
            };
        };
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Cloud"];
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
    connectNamespace: {
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
                "application/json": components["schemas"]["ConnectNamespace"];
            };
        };
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Cloud"];
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
    connectNovita: {
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
                "application/json": components["schemas"]["ConnectE2BCompatible"];
            };
        };
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Cloud"];
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
    connectRunloop: {
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
                "application/json": components["schemas"]["ConnectRunloop"];
            };
        };
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Cloud"];
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
    connectTenki: {
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
                "application/json": components["schemas"]["ConnectTenki"];
            };
        };
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Cloud"];
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
    disconnectCloud: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                org: string;
                /** @description Cloud name */
                cloud: "aws" | "gcp" | "azure" | "daytona" | "runloop" | "e2b" | "novita" | "lelantos" | "namespace" | "tenki" | "modal";
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
    listEvents: {
        parameters: {
            query?: {
                /** @description Only events of this level */
                level?: "info" | "warning" | "error";
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
                    "application/json": components["schemas"]["EventList"];
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
    listMembers: {
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
                    "application/json": components["schemas"]["MemberList"];
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
    previewMigration: {
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
                "application/json": components["schemas"]["MigrationPlan"];
            };
        };
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["MigrationPreview"];
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
    openMigrationPullRequest: {
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
                "application/json": components["schemas"]["MigrationPlan"];
            };
        };
        responses: {
            /** @description Created */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["MigrationPullRequest"];
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
    scanRepositoryWorkflows: {
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
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["WorkflowScan"];
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
    getSettings: {
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
                    "application/json": components["schemas"]["Settings"];
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
    updateSettings: {
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
                "application/json": components["schemas"]["Settings"];
            };
        };
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Settings"];
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
    runnerCacheGrant: {
        parameters: {
            query?: never;
            header?: {
                /** @description Bearer runner cache token */
                Authorization?: string;
            };
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
                    "application/json": components["schemas"]["Grant"];
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
