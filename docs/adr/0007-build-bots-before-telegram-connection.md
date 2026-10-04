# Build Bots before connecting Telegram

Piko users can start a Piko chat, request a Bot, and build and preview its Draft before supplying a Telegram token. Once created, the Bot is the owner-owned configuration throughout this process; we choose this over a separate build project that must later be converted or copied into a connected Bot, accepting changes to Bot storage and lifecycle boundaries to support chat-first creation.

Create Bot opens Piko's conversational experience directly, with selectable example prompts illustrating supported capabilities. Owners describe their idea in the conversation rather than completing a separate idea or naming form; Piko can suggest an editable Bot name as the conversation develops. Telegram connection remains a later step.

Opening Piko, sending a general question about the application, or selecting an example prompt does not create a Bot. Bot creation requires the owner's request to build one; selectable examples fill the composer for the owner to personalize and submit.

An explicit creation request produces a Bot only after a valid initial Draft is ready. Until then, the saved Piko chat displays the work in progress; failure or Stop preserves the conversation without creating a Bot. The same conversation becomes the new Bot's Builder chat under ADR 0012.

An Unconnected Bot has no Telegram identity and is distinct from a Disconnected Bot, which retains a previously verified identity under ADR 0006. Connecting Telegram later supplies verified identity and credentials without making the Draft live; the owner explicitly deploys it under ADR 0004. Existing connected Bots, retained identities, publications, interactions, and Submissions must survive the forward migration.
