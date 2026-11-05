# Profile environment and credential files

Each profile's `credentialsSecret` selects a dotenv file mounted only into that
profile's home on the gateway. Hermes v0.22.7 loads it into the active profile secret
scope. Do not use `envFrom` to put every profile's credentials into the shared process.

```yaml
spec:
  credentialsSecret:
    name: profile-credentials-assistant
    key: .env
  config:
    terminal:
      env_passthrough:
      - SERVICE_TOKEN
      credential_files:
      - service.json
```

Store `SERVICE_TOKEN` in the selected Secret's `.env` value. Hermes selects allowed
names in the active profile scope, supplies the SSH client environment and sends
`SendEnv` requests. The controller-generated sshd configuration uses `AcceptEnv *`;
Hermes owns the allowlist and its provider/internal credential exclusions. Another
profile must explicitly configure its own source and allowed names. OpenSSH clears
ordinary Pod environment before starting the session.

`terminal.credential_files` uses native SSH synchronization (Docker uses read-only
mounts when that backend is chosen). Files must exist beneath the profile home.
Consumers must read them explicitly; transmitting a dotenv file does not source or
export it. No custom envfile renderer, sshd wrapper or Secret merger is installed.

## Existing declarations

Deprecated `spec.env` remains in the shared CRD for independently pinned stable
controllers. This controller still injects those entries into the terminal Pod,
but no longer bridges them into SSH sessions. To migrate an SSH consumer, put its
values into that profile's dotenv credential Secret and its names into
`spec.config.terminal.env_passthrough`; then remove its old `spec.env` declarations.
This changes the credential holder from terminal-only to the gateway profile.
Do not rewrite unrelated profile credentials or inject all profiles globally.
