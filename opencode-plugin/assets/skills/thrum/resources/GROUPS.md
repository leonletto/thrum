# Groups

Groups enable sending messages to collections of agents.

## Built-in Groups

- **@everyone** — Auto-created, contains all registered agents. Cannot be
  deleted.

## Creating Groups

```bash
thrum group create backend-team --description "Backend developers"
thrum group create leads --description "Team leads"
```

## Adding Members

Two member types: agents and roles.

```bash
thrum group add backend-team @alice            # Specific agent
thrum group add backend-team --role implementer # All agents with role
```

## Sending to Groups

```bash
thrum send "Sprint planning" --to @backend-team
thrum send "All-hands update" --to @everyone
```

Group membership is resolved at read time (pull model). If a new agent joins
with `--role implementer`, they automatically receive messages sent to groups
that include that role.

## Viewing Groups

```bash
thrum group list                               # All groups
thrum group info backend-team                  # Group details
thrum group members backend-team --expand      # Resolved to agent IDs
```

## Removing Members

```bash
thrum group remove backend-team @alice
thrum group delete old-team
```
