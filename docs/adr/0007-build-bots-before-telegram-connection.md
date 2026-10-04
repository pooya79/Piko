# Build Bots before connecting Telegram

Bot owners can start a Builder chat, create a Bot, and build and preview its Draft before supplying a Telegram token. The Bot is the owner-owned configuration throughout this process; we choose this over a separate build project that must later be converted or copied into a connected Bot, accepting changes to Bot storage and lifecycle boundaries to support chat-first creation.

An Unconnected Bot has no Telegram identity and is distinct from a Disconnected Bot, which retains a previously verified identity under ADR 0006. Connecting Telegram later supplies verified identity and credentials without making the Draft live; the owner explicitly deploys it under ADR 0004. Existing connected Bots, retained identities, publications, interactions, and Submissions must survive the forward migration.
