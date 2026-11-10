# ntfy MessagingChannel

MessagingChannel supports exactly one platform block: telegram or ntfy. The profile claims one channel; replacing its claim does not enable both platforms. Disable the old platform explicitly in profile config and retire its encrypted token reference in deployment Git.

For ntfy set spec.ntfy.serverURL and spec.ntfy.topic. Topics follow `ha[A-Za-z0-9]{12}_{channel-name-in-kebab-case}`; generate the random prefix once with a cryptographic generator and persist the topic in Git, rather than regenerating on reconciliation. Optional tokenSecretRef projects a token file and triggers rollout on credential changes; tokenless internal connections rely on receiver mesh identity authorization. Public clients still use the existing Cloudflare device-certificate policy.

Gateway init replaces stale NTFY_* and TELEGRAM_* credential settings with the selected ntfy channel configuration, topic allowlist, home channel and Markdown support. The original profile credential Secret is unchanged. Official Hermes ntfy adapter handles subscriptions and publishing, including its own echo filtering; no bot-to-bot behavior is added here.

Canary acceptance must publish an ordinary message and observe an actual tagged Hermes reply on the same topic, verify ntfy connection without transport errors and verify Telegram is disabled. Pod readiness alone is insufficient. Production stays on its existing channel until canary acceptance.
