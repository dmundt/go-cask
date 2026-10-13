---
type: Guide
title: Windows development with WSL, WSLC and VS Code — go-cask
description: Install WSL Containers without a Linux distribution and use WSLC with VS Code for go-cask development.
version: v3
---

# Windows development with WSL, WSLC and VS Code

WSL Containers (WSLC) provides the `wslc.exe` container CLI as part of WSL.
You do not need to install Ubuntu or another Linux distribution. This guide
covers setting up WSLC on Windows, using it from VS Code, and optionally
opening go-cask's development environment as a VS Code dev container.

## Purpose and workflow choices

WSLC is the container runtime; a dev container is the project's configured
development environment. WSLC can run containers directly, while VS Code's
Dev Containers extension can use WSLC to create the Debian environment defined
by this repository's [`.devcontainer/devcontainer.json`](../.devcontainer/devcontainer.json).

### WSLC advantages

- WSL provides the Linux container runtime without requiring a separate Linux
  distribution or a separately installed container engine.
- Use familiar container operations from Windows PowerShell and access
  published container ports through localhost.
- Lightweight for running disposable Linux tools or containers without
  changing the Windows Go toolchain or installing Linux packages on Windows.

### WSLC trade-offs

- WSLC provides the runtime, not a complete project-specific toolchain. A
  plain container does not automatically include go-cask's pinned Go, gate
  tools, or repository settings.
- WSLC is new, and VS Code's Dev Containers extension does not officially
  support every Docker-compatible CLI. Some extension workflows may not work
  with WSLC.
- Running the repository directly with Windows tools is not equivalent to
  running its Linux race and coverage gate in the Debian environment.

### Why use this repository's dev container?

- It gives contributors the same Debian 13 base and pinned Go 1.27.2,
  golangci-lint, GitHub CLI, PowerShell, and Python setup declared by the repo.
- The Linux environment includes the compiler and runtime needed by the race
  and coverage checks; host-specific packages and versions are less likely to
  affect results.
- VS Code attaches to the configured workspace, so its terminal, Go tooling,
  and gate commands run inside the development environment. Use
  `./scripts/verify.sh` there for the supported repository gate.
- This is the recommended path for building, testing, and validating go-cask.
  Use direct WSLC commands when you specifically want to experiment with
  containers outside the repository's preconfigured environment.

## 1. Install or update WSL and WSLC

Open **PowerShell as Administrator** on Windows. If WSL is not installed,
install it without a Linux distribution:

```powershell
wsl --install --no-distribution
```

Restart Windows if prompted. Install or update WSL to a version that includes
WSLC:

```powershell
wsl --update
wsl --version
```

WSLC requires WSL 2.9.3 or newer. Verify the CLI and run its smoke test:

```powershell
wslc version
wslc run --rm hello-world
```

No Ubuntu first-run setup, Linux username, or `apt` installation is needed.
For installation details, see Microsoft's
[WSL installation guide](https://learn.microsoft.com/windows/wsl/install)
and [WSL container overview](https://learn.microsoft.com/windows/wsl/wsl-container).

## 2. Use WSLC from Windows VS Code

1. Install [Visual Studio Code for Windows](https://code.visualstudio.com/).
2. Clone or open the repository in VS Code as a Windows folder.
3. Open a **PowerShell** integrated terminal at the repository root.
4. Run WSLC commands from that terminal. For example, check images and
   containers with:

   ```powershell
   wslc image list
   wslc container list --all
   ```

For container build and run examples, see Microsoft's
[WSL container tutorial](https://learn.microsoft.com/windows/wsl/tutorials/wsl-containers).

## 3. Open go-cask in a VS Code dev container

The repository includes [`.devcontainer/devcontainer.json`](../.devcontainer/devcontainer.json)
for a Debian 13 development environment with Go and Linux PowerShell (`pwsh`).
To open it through WSLC:

1. Install Microsoft's **Dev Containers** VS Code extension
   (`ms-vscode-remote.remote-containers`).
2. In VS Code user settings, set the container CLI path:

   ```json
   {
     "dev.containers.dockerPath": "wslc.exe"
   }
   ```

3. Open the repository folder in VS Code, then run **Dev Containers: Reopen in
   Container** from the Command Palette.
4. When attached, confirm the integrated terminal is Linux PowerShell with
   `pwsh`, and run the repository gate from the workspace root:

   ```powershell
   ./scripts/verify.sh
   ```

VS Code supports Docker-compatible CLIs through `dev.containers.dockerPath`,
but does not officially support every alternate CLI. If the Dev Containers
extension reports an unsupported WSLC command, use the direct WSLC workflow
above for containers and the repository's Debian dev container from an
environment that supports it. See VS Code's
[alternate Docker CLI guidance](https://code.visualstudio.com/remote/advancedcontainers/docker-options).

The gate is green only when it exits successfully and ends with
`verification passed`. See [`scripts/AGENT.md`](../scripts/AGENT.md) for
gate-receipt and signing details.

If WSL stops responding after an update, close active containers and run
`wsl --shutdown` in Windows PowerShell, then rerun the WSLC smoke test.
