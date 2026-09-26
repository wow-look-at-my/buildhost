# Run locks

A run lock holds the first value a GitHub Actions run records under a name. Every later job of that run reads the same value. The gosmopolitan go command uses it to lock the branch head of each org module for a run, so a commit that lands mid-run reaches no job of it.

## Contract

- A lock belongs to one attempt of one workflow run of one repository. The key is the repository id, `run_id` and `run_attempt` from the caller's verified OIDC token, plus a name.
- A lock is written once and never changes. Of racing claims exactly one writes, and every claim answers with the winner.
- A lock lives for `db.RunLockLifetime`, the longest a workflow run can last. Each claim deletes the locks that are older.
- A run lock is not a release, a project or an artifact. Nothing downloads it, and retention does not see it.

## Authentication

The caller presents the job's GitHub Actions OIDC token, minted with the server URL as its audience. No workflow adds a secret: `permissions: id-token: write` is enough. The token passes the same issuer, org and event checks as an auto-provisioning publish. A static API token names no run. As a result, it is refused.

The request also names the run as `repository`, `run_id` and `run_attempt`. A request that names another run than its token is refused with 403, rather than served from the token's run.

## Endpoints

`GET /api/v1/run-locks?repository=&run_id=&run_attempt=&name=` answers `{"found": bool, "value": string}`.

`POST /api/v1/run-locks` takes the same fields and a `value` as JSON. It answers `{"found": true, "value": string, "created": bool}`, where `value` is what the run holds after the claim.

A name and a value are each at most `maxRunLockField` bytes.
