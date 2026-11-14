# kargo-loki-logs

An adapter that implements [Kargo](https://kargo.io)'s AnalysisRun log API
(`api.rollouts.logs.urlTemplate`) with [Grafana Loki](https://grafana.com/oss/loki/)
behind it. It returns a verification Job's log lines as plain text, so the Kargo UI
shows readable logs instead of Loki's JSON.

Status: under construction.

## Contributing

Issues are welcome. Outside pull requests are not accepted; a pull request from
outside the project is closed with a pointer to the issue tracker.

## License

[MIT](LICENSE)
