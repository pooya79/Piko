# Let a Piko chat become one Bot's Builder chat

A Piko chat is owned by its Piko user and can answer questions about the application before any Bot exists. On an explicit build request, Piko stages a validated initial Draft; successful completion atomically creates the Bot and Draft, associates that same conversation with the Bot, and saves the result. We choose this over handing the owner into a separate conversation to preserve continuity, accepting changes to chat ownership and run scope beyond the Bot-only model in ADR 0008.

Capability questions produce answers without Bot creation; ambiguous intent receives a clarifying question. General runs have no Draft-editing capability. An explicit build request permits staging creation but does not itself persist a Bot; failure or Stop before the successful commit retains the chat without a Bot. Durable request deduplication and guarded completion prevent repeat submissions from creating duplicate Bots.

After association, the chat works on that Bot's shared Draft and retains only its own conversation memory. It cannot be retargeted to another Bot; creating another Bot starts a new Piko chat. Product examples fill the composer rather than submitting automatically, and the existing explicit deployment boundary remains in place.

Deleting the Bot deletes all of its associated chats, including their earlier general discussion, under ADR 0006. The confirmation explains this scope; general Piko chats that have never become Builder chats remain independent of Bot deletion.
