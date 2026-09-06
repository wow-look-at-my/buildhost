# Handing a published site to the pull request that previews it

`buildhost-publish-site` posts the site URL as a comment on the open pull
request for the branch it published. `.github/actions/lib/preview-comment.ts`
holds the whole mechanism, and `test/actions/preview-comment.test.ts` holds its
assertions.

## Why the publish owns it

Publishing per branch pays off when a reviewer opens the preview before
merging. A URL that reaches nobody buys nothing, and every consumer that wanted
one used to write its own comment step. A reusable workflow in
`wow-look-at-my/actions` carried a second implementation of the same comment,
and it could only deploy a sparse checkout or a run artifact. The org publishes
without GitHub Actions artifacts, so a consumer that builds its own output had
no route through it and wrote a third. That workflow is deleted, and its
callers publish through this action directly.

One publish, one comment.

## What it posts

One comment per project and site branch, marked
`<!-- buildhost-preview:<project>/<branch> -->`. A later publish of the same
site edits that comment rather than adding another. A repository that publishes
two sites off one commit, such as a library and a component gallery, gets one
comment for each, because the marker carries the site branch.

## Which pull request

A `pull_request` event names its own number. A push event does not, so the
branch names it: the open pull request whose head is this branch is the one a
reviewer is reading. A branch with no open pull request is a no-op that logs
and returns, because a push to the default branch previews nothing anybody is
waiting on.

## Failure

A refused write fails the publish and names the grant. The job must declare
`pull-requests: write`, and a job-level `permissions:` block replaces the
workflow-level one. `comment: 'false'` is the way to publish without posting,
and it is a decision the workflow states rather than a permission it omits. A
missing grant would otherwise read as a preview that quietly stopped reaching
anybody.
