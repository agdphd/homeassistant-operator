# Contributing to Home Assistant Operator

Thank you for your interest in the project! Community contributions are very welcome.

## How Can I Help?

### Opening Issues

The best way to contribute is by **opening an Issue** on GitHub:

- **Feature requests** - Have an idea for a new feature? Open an Issue!
- **Bug reports** - Found a bug? Let us know!
- **Questions** - Have questions? Open an Issue

Every submission is valuable and helps us develop the project in the right direction.

## Pull Requests

### Rules

1. **Fork & PR** - Work on your fork and submit Pull Requests
2. **One PR = one change** - Don't mix unrelated changes in one PR
3. **Tests** - Make sure `make test` passes
4. **Linting** - Make sure `make lint` reports no errors
5. **Security** - Run `make security-check` before submitting (CI will block PRs with vulnerabilities)



## Development Setup

### Prerequisites

```bash
# Requirements
# - Go 1.25+
# - Docker
# - kubectl
# - k3d (for local testing)
# - pre-commit (for commit hooks)

# Install pre-commit hooks
pre-commit install
pre-commit install --hook-type commit-msg
```

### Building and Testing

```bash
# Build
make build

# Tests
make test

# Linting
make lint

# Security scanning
make security-check

# Local testing with k3d
make k3d-create
make test-k3d
```

### Pre-commit Hooks

This project uses [pre-commit](https://pre-commit.com/) to automatically check and fix code before commits.

**Automatic checks on every commit:**
- Code formatting (gofumpt, goimports)
- Linting (golangci-lint)
- YAML validation
- Commit message validation (conventional commits)
- Secrets scanning

**Manual checks:**

```bash
# Run all pre-commit hooks manually
pre-commit run --all-files

# Run specific hook
pre-commit run golangci-lint --all-files

# Update hook versions
pre-commit autoupdate
```

## Code of Conduct

Be nice and respect others. This is a simple project and we want to keep a friendly atmosphere.
