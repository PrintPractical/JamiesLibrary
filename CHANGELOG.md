# Changelog

All notable changes to this project are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project
follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Test seams for the test suites: one clock source (`internal/clock`), test hooks compiled only with `-tags testhooks`, an in-process test server and a fake Open Library (`internal/testutil`).
- Project skeleton: a Go server with a health endpoint, the embedded web app, and the repository check.
