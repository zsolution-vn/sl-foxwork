# Mattermost HA on Docker Swarm

This example shows how to run the Mattermost HA build on Docker Swarm with
automatic gossip discovery. It relies on Docker's `tasks.<service>` DNS entry,
which always returns the IP addresses of every replica in the service. The
custom entrypoint script injects that DNS name into
`MM_CLUSTERSETTINGS_GOSSIPPEERADDRESSES`, so each replica joins the memberlist
ring without maintaining a static seed list.

## Prerequisites

- Docker Engine with Swarm mode initialised (`docker swarm init`).
- The `mattermost-ha:latest` image available on every Swarm node.
  Build it from the repository root if one isn’t already published:

  ```bash
  docker build -t mattermost-ha:latest .
  ```

## Deploy

1. Create the Swarm configs and secrets referenced by `stack.yml` (none are
   required beyond the script config bundled with the stack).

2. Deploy the stack:

   ```bash
  cd deploy/docker/swarm
  docker stack deploy -c stack.yml mmha
   ```

   Swarm launches the Postgres instance and three Mattermost replicas. Each
   Mattermost container executes `entrypoint-swarm.sh`, which sets
   `MM_CLUSTERSETTINGS_GOSSIPPEERADDRESSES` to `["tasks.mm-ha-app:7946"]`.
   Docker’s embedded DNS resolves that hostname to all current replicas, so any
   scale-up events automatically join the gossip cluster.

3. Verify cluster membership:

   ```bash
   docker service ps mmha_mm-ha-app
   ```

   Within Mattermost logs you should see gossip join events and DB lease
   leadership elections.

## Scaling

The service definition specifies the gossip port (TCP/UDP) and uses the DNS
alias as the seed. Scaling is just:

```bash
docker service scale mmha_mm-ha-app=5
```

New replicas resolve `tasks.mm-ha-app` and join the gossip ring immediately,
without configuration changes. When replicas exit, memberlist broadcasts leave
events so the cluster shrinks cleanly.

## Notes

- Ensure the overlay network (`ha`) spans every node where replicas may run.
- State (Postgres data, Mattermost file uploads) is stored in named volumes
  (`db-data`, `app-data`). Point them at durable storage in production.
- The stack pins the Postgres service to a manager for simplicity; adjust
  placement constraints as needed.



