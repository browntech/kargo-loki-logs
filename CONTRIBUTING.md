# Contributing

Thank you for your interest in kargo-loki-logs.

## Issues

Issues are welcome. Open one for a bug, a question, or a feature request.

For a bug, include:

- the kargo-loki-logs version (image tag or chart version),
- the Kargo and Loki versions,
- the `/logs/...` path Kargo requested and the HTTP status it got back,
- the adapter's warning and error lines, or its output with `LOG_LEVEL=debug`.

Remove the `Authorization` header and any token before you post.

## Pull requests

Outside pull requests are not accepted. Only collaborators can open a pull request
in this repository. If you have a fix or an idea, describe it in an issue.

A collaborator's pull request uses the
[pull request template](.github/pull_request_template.md): summary, changes,
testing and checklist.

## Releases

Only repository admins can push a `v*.*.*` tag; a tag ruleset enforces this.

1. Push the release commit to `main` first. The release workflow fails a tag whose
   commit is not on `main`. If you pushed the tag first, push `main` and re-run the
   failed jobs.
2. Push a `vX.Y.Z` tag on that commit.
3. Approve the run. Every publish job waits for approval in the `release` environment,
   which needs a required reviewer.

The workflow runs from the tagged commit, so its checks are only as strong as the
rule on who can push a tag. Keep tag push limited to admins.

## Security

Do not open a public issue for a security problem. Report it privately on the
repository's **Security** tab: **Report a vulnerability**. See [SECURITY.md](SECURITY.md).

## License

kargo-loki-logs is released under the [MIT License](LICENSE).
