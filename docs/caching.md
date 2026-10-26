# Caching

`extra_config.cache: true` gives a runner drop-in `actions/cache`
acceleration backed by an S3 bucket in your own account, in the runner's
region. `actions/cache`, `actions/setup-*` (`cache: npm` and friends),
`Swatinem/rust-cache`, `astral-sh/setup-uv` and anything else built on
`@actions/cache` work unchanged, with no size limit beyond your bucket.

```yaml
runners:
  - name: build
    cloud: aws
    instance_type: c6gn.4xlarge
    region: eu-west-1
    labels: [cirun-build]
    extra_config:
      cache: true
      BlockDeviceMappings:
        - VolumeSize: 100
          VolumeType: gp3
          Iops: 6000
          Throughput: 1000      # gp3 defaults to 125 MiB/s; the disk is the usual bottleneck
```

`docker/build-push-action` needs the `url_v2` form:

```yaml
- uses: docker/setup-buildx-action@v3
  with:
    driver-opts: network=host
- uses: docker/build-push-action@v6
  with:
    cache-from: type=gha,url_v2=http://127.0.0.1:6420/
    cache-to: type=gha,mode=max,url_v2=http://127.0.0.1:6420/
```

## How it works

1. At launch, the control plane makes sure the bucket
   `airship-cache-<region>-<hash>` exists in your account: private, with a
   lifecycle rule that expires entries after 7 days (and abandoned multipart
   uploads after a day). It gives the machine a one-off `asr_` token.
2. The boot script downloads the cache proxy from the control plane, starts
   it, and trusts its certificate authority on the machine only (system
   store and `NODE_EXTRA_CA_CERTS`). The runner's `.env` gets
   `https_proxy=http://127.0.0.1:6421` and `no_proxy=127.0.0.1,localhost`,
   which the runner passes to every step.
3. The proxy trades its token for credentials from
   `GET /v1/runner/cache-grant`: an STS federation token (from your access
   key) allowed to read and write only this repository's prefix in the
   bucket, valid for an hour and refreshed before it expires. Your access
   key never reaches the machine.
4. `actions/cache` talks to `results-receiver.actions.githubusercontent.com`
   through the proxy. The proxy terminates TLS for that host, answers the
   `CacheService` calls itself (reserve, upload, finalize, lookup) and
   forwards everything else on that host (artifacts, job results) to
   GitHub untouched. Every other host is tunnelled without inspection.
5. Uploads arrive as Azure block blob calls against the "signed" URL the
   proxy hands out; it stages the blocks on the root volume and writes the
   entry to S3 as a multipart upload with 8 parallel 64 MiB parts.
   Downloads are served with the HEAD plus ranged GETs the client makes,
   straight from S3.

Entries are stored at `<owner>/<repo>/<version>/<key>`. `restore-keys`
match by prefix within the same version, newest first, as on GitHub. The
proxy also serves the legacy `_apis/artifactcache` API on port 6420 for
older actions.

If any step fails (the binary is not embedded in this control plane build,
the bucket cannot be created, the proxy does not start) the job still
runs, without acceleration, and the **Events** page records why.

## IAM permissions

In addition to `AmazonEC2FullAccess`, the connected IAM user needs:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "CacheBuckets",
      "Effect": "Allow",
      "Action": [
        "s3:CreateBucket",
        "s3:ListBucket",
        "s3:GetBucketLocation",
        "s3:PutBucketPublicAccessBlock",
        "s3:PutLifecycleConfiguration"
      ],
      "Resource": "arn:aws:s3:::airship-cache-*"
    },
    {
      "Sid": "CacheObjects",
      "Effect": "Allow",
      "Action": [
        "s3:GetObject",
        "s3:PutObject",
        "s3:AbortMultipartUpload",
        "s3:ListMultipartUploadParts"
      ],
      "Resource": "arn:aws:s3:::airship-cache-*/*"
    },
    {
      "Sid": "RunnerCredentials",
      "Effect": "Allow",
      "Action": "sts:GetFederationToken",
      "Resource": "*"
    }
  ]
}
```

A federation token can never exceed the user's own permissions, and the
proxy's policy narrows it to one prefix, so the object permissions above
are the ceiling of what a runner can do.

## Things to know

- **Throughput.** Set gp3 `Throughput: 1000` and `Iops` to match on the
  root volume; the default 125 MiB/s caps restores well below what the
  network does. Instance class is the next lever.
- **Scope.** Unlike GitHub's cache there is no per-branch isolation inside
  a repository: a pull request's job can write an entry a main-branch job
  restores. Keep public repositories with fork pull requests off cache
  runners, or make their keys unguessable.
- **Containers.** Steps in a `container:` job inherit `https_proxy` but not
  the host's trust store; `NODE_EXTRA_CA_CERTS` points at a host path. Run
  cache steps on the host.
- **Not run against a real runner yet.** The protocol handling is tested
  against the clients' documented behaviour and fakes. The first real job
  is the first end-to-end run; the Events page and
  `/var/log/airship-cacheproxy.log` on the machine show what happened.
- **BYO bucket.** For runners outside this setup, Cirun's
  `cirunlabs/cache/s3` action works against any S3-compatible bucket with
  static keys from repository secrets.
