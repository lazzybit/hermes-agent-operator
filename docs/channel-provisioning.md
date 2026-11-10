# Channel provisioning

Profile consumes `spec.messaging.claimName`. ChannelClaim requests only generic
properties (`name`, `displayName`, class name, optional static channel name). A nonempty
class selects the Telegram provisioner; an explicit empty class disables dynamic
provisioning. Claims report only Pending or Available. Available means reciprocal
UID binding and a nonempty credential Secret; it does not prove message delivery.

ChannelClass parameters hold the manager token Secret reference, user session
Secret reference, and required `usernameTemplate`. Only `{RANDOMIZED}` and `{NAME}`
are supported; for example `ha{RANDOMIZED}_{NAME}_bot`. `NAME` comes from Claim
`spec.name` (kebab case); Telegram converts hyphens to underscores. `RANDOMIZED`
is 16 cryptographically random characters from `A-Z`, `a-z` and `0-9`, sampled
uniformly and independently of the Claim UID. Persist the generated username
before external calls and reuse it on retries. No suffix is appended and overlong
results are rejected without truncation.
Existing persisted identities are retained. Session Secret keys: `api_id`,
`api_hash`, `session` (Telethon StringSession). Manager token alone cannot invoke
user-only `bots.createBot`. The controller does not prompt for login or fall back
to user confirmation. Missing user authorization reports UserSessionRequired.

The MTProto helper receives credentials over stdin, checks authorization, resolves
the manager capability, creates/re-discovers a persisted generated username,
validates manager and current-user ownership, and returns identity over a private
pipe. Controller uses manager HTTP Bot API to retrieve and verify child token.
Secret and Channel use Claim UID-based names and are retained without GC owners.
Existing usernames are never adopted solely by name. Class changes do not rename
already-bound bots. Polling of manager updates is unnecessary for this MTProto
path. Legacy MessagingChannel stays supported for production migration safety;
new claims never consume legacy channels automatically.

Initial scope: namespaced resources; Telegram only; Retain only; one Profile
consumer (stable name ordering). No default class selection, automatic static
matching, user-confirmed flow or external bot deletion.

Validation: Go API/controller tests, go vet, Python helper ownership/auth tests,
image smoke tests. Live child provisioning requires an authorized user session.
