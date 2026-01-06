# Contributing to Home Assistant Operator

Thank you for your interest in the project! Community contributions are very welcome.

## How Can I Help?

### Opening Issues

The best way to contribute is by **opening an Issue** on GitHub:

- **Feature requests** - Have an idea for a new feature? Open an Issue!
- **Bug reports** - Found a bug? Let us know!
- **Questions** - Have questions? Open an Issue or use GitHub Discussions

Every submission is valuable and helps us develop the project in the right direction.

## Pull Requests

### Rules

1. **Fork & PR** - Work on your fork and submit Pull Requests
2. **One PR = one change** - Don't mix unrelated changes in one PR
3. **Tests** - Make sure `make test` passes
4. **Linting** - Make sure `make lint` reports no errors



## Development Setup

```bash
# Requirements
# - Go 1.22+
# - Docker
# - kubectl
# - k3d (optional, for local testing)

# Build
make build

# Tests
make test

# Linting
make lint

# Local testing with k3d
make k3d-create
make test-k3d
```

## Code of Conduct

Be nice and respect others. This is a simple project and we want to keep a friendly atmosphere.

## Questions?

Open an Issue or reach out via GitHub Discussions.

---

Thank you for every contribution!
