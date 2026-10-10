# Contributing to KeiRouter

Thanks for your interest in contributing! This document covers everything you need to get started.

## Prerequisites

- **Go 1.24+**
- **Node.js 20.19+** and npm
- **Git**

## Development setup

```bash
# Clone the repo
git clone https://github.com/mydisha/keirouter.git
cd keirouter

# Install dependencies
make install

# Run backend + frontend together (hot reload)
make dev
```

This starts the backend on `:20180` and the dashboard on `:5180`.

### Useful commands

| Command | What it does |
|---|---|
| `make dev` | Run backend + frontend concurrently |
| `make backend` | Run only the Go backend |
| `make frontend` | Run only the Vite dev server |
| `make build` | Build production binary + frontend assets |
| `make test` | Run the backend test suite |
| `make vet` | Run Go static analysis |
| `make bootstrap` | Create an initial API key |

## Making changes

1. **Fork** the repo and create a branch from `main`:
   ```bash
   git checkout -b feat/my-feature
   ```

2. **Make your changes.** Keep commits focused and well-described.

3. **Run checks before pushing:**
   ```bash
   make test          # Go tests
   make vet           # Go static analysis
   cd frontend && npm run lint && npm run typecheck
   ```

4. **Open a Pull Request** against `main`. Fill out the PR template.

## Code style

- **Go:** Follow standard Go conventions. Run `go vet` and `gofmt`.
- **TypeScript:** The project uses ESLint + TypeScript strict mode. Run `npm run lint` and `npm run typecheck` in the `frontend/` directory.
- **Commits:** Use concise, descriptive messages. Prefix with the area of change when helpful (e.g. `gateway: fix streaming chunk encoding`).

## Project structure

```
backend/
  cmd/keirouter/     entrypoint
  internal/          all Go packages (see README architecture section)
frontend/
  src/               React + TypeScript dashboard
deploy/              Dockerfile + compose
```

## Reporting bugs

Open a [GitHub Issue](https://github.com/mydisha/keirouter/issues/new?template=bug_report.md) with steps to reproduce, expected behavior, and your environment.

## Suggesting features

Open a [GitHub Issue](https://github.com/mydisha/keirouter/issues/new?template=feature_request.md) describing the problem you want solved and your proposed approach.

## Security

See [SECURITY.md](SECURITY.md) for reporting vulnerabilities.

## License

By contributing, you agree that your contributions will be licensed under the [MIT License](LICENSE).
