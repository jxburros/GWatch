# Plan: watching containers and VMs

Status: proposal. Nothing here is implemented.

## Question

Should GWatch get a new kind of node for containers (a "Docker" node with a
check per container), and can the same idea cover VMs that share one piece of
hardware?

## What GWatch already does

- **Dependencies.** A node can have a parent node (`DependsOnNode`,
  `internal/model/model.go`). A VM or container node can depend on its host
  node. When the host goes down, child alerts are suppressed ("affected by
  parent") and the network map traces the outage. This already works for any VM
  with its own IP and any container with a published port, using ping, TCP,
  HTTP and the other check types.
- **Agent inside a container.** An agent running inside a container reports its
  cgroup memory and CPU limits (`HostMetrics.Container`,
  `internal/model/host.go`). That is one container at a time, and only if the
  agent is installed in each one.
- **Services and programs.** Named systemd/Windows services and named processes
  can be watched (`service:` and `process:` readings). This does not extend to
  Docker containers or hypervisor guests.
- **Docker in the docs.** The docs cover running GWatch in Docker
  (`docs/DOCKER.md`) and restarting a container from a trigger
  (`docs/RECIPES.md`). Nothing asks the Docker daemon what is running.

## What is missing

- No way to see "all containers on this host" with state, health, restart count
  and resource use.
- No way to see "all VMs on this machine" from a hypervisor.

## Decision

**Do not add a new node kind.** A node is a group of checks plus a parent link.
A "Docker host" is an ordinary node with a Docker check on it, and a VM is an
ordinary node whose parent is the hypervisor host. Instead, add new **reading
types** next to `service:` and `process:`, so each container or VM gets the same
per-reading value, status, threshold, chart, incident and trigger variable that
every other reading already has.

## Phases

### 1. `docker:` readings

- The agent (and the local collector) lists containers through the Docker
  socket, read-only.
- In the node editor the user loads the container list and ticks the ones to
  watch, as the service and process pickers do.
- Per container: running/stopped state, health-check status, restart count,
  memory and CPU use against its limit.
- Only the ticked names are collected, so hosts that nobody asked about pay
  nothing (the same rule `CollectSelection.Services` and `.Processes` follow).
- Podman exposes a compatible API and should work with the same code.
- Docs must say what socket access means: read-only access to
  `/var/run/docker.sock` still lets the reader see everything on the daemon, so
  the agent's account needs to be chosen deliberately, and a GWatch container
  needs the socket mounted.
- Tests: collector parsing from recorded API responses, selection filtering,
  and the verdict for stopped, unhealthy and restart-looping containers.

### 2. `vm:` readings, one backend at a time

- Same shape as `docker:`: a reading per ticked guest, collected on the
  hypervisor host.
- **Hyper-V goes first**, because that is what the owner runs. Later, in order
  of likely use: libvirt (`virsh`/the libvirt API), then the Proxmox API. ESXi
  last, if at all.
- Hyper-V: the gwatch-agent runs on the Windows host and lists guests through
  WMI (`root\virtualization\v2`, `Msvm_ComputerSystem`) or, failing that,
  PowerShell's `Get-VM`. Prefer WMI, since the agent already avoids shelling out
  for its other Windows readings. The agent's account needs membership of the
  local "Hyper-V Administrators" group (or equivalent read rights), and the docs
  must say so.
- Per guest: state (running, off, paused, saved, starting, stopping), uptime,
  and CPU and memory use where Hyper-V exposes them (a guest that is off has
  none).
- Guests with their own IP also get ordinary checks (ping, TCP, HTTP) on a child
  node whose parent is the Hyper-V host, so the host going down suppresses their
  alerts.
- Tests: parsing from recorded WMI/PowerShell output, selection filtering, and
  the verdict for off, paused and saved guests.

### 3. Optional: create child nodes from discovered guests

- For a guest with its own IP, offer to create a node with the host as parent,
  so dependency suppression and the network map work with no manual wiring.
- Skip it at first. Readings alone cover the need, and this can follow once
  they exist.

## Out of scope

- Starting, stopping or restarting guests from GWatch. Triggers already run
  `docker restart` or `ssh ... virsh` where wanted (`docs/RECIPES.md`).
- Orchestrators (Kubernetes, Swarm) and cloud hypervisors.

## Open questions

- Does the owner want a guest that is deliberately off to count as down? Likely
  an opt-in per ticked guest.
- Should the Docker reading run only through the agent, or also from the local
  collector when GWatch itself is on the Docker host?
