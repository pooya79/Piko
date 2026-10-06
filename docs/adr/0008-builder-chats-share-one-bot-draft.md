# Give each Builder chat its own memory and share one Bot Draft

A Bot has one Draft and can have multiple persistent Builder chats. Each chat uses only its own conversation history and the current Bot Draft, rather than inheriting another chat's messages or maintaining a separate deployable Draft. This supports separate discussions without creating competing versions for the owner to select at deployment, accepting the need to coordinate edits to shared state.

ADR 0012 extends the conversation model with Piko chats that exist before a Bot. After successful creation, the original Piko chat becomes a Builder chat for that one Bot and retains its own earlier history; it cannot be retargeted to another Bot. The shared Draft and per-chat memory rules continue to apply after this transition.

Chat history survives refresh, logout, and server restarts. An owner can delete a Builder chat without deleting the Bot or reverting its Draft changes; deleting local chat history does not request removal of previously exported history from Langfuse.

Full messages remain available for display. Model context uses a summary of older messages, recent messages, and the current Draft; summaries remain private to their originating Builder chat.

The Builder agent may add, edit, and remove approved Blocks in response to owner requests, but cannot execute generated code or introduce unsupported integrations under ADR 0002. A turn's Draft changes are validated and applied together; failed turns preserve the previous Draft. Successful turns support Undo. Publication and Telegram activation remain explicit owner actions under ADR 0004.

Only one Builder run can be active for a Bot; requests from its other chats receive a busy response rather than being queued. The standalone manual editor has been removed. The shared revision guard remains: an agent result cannot overwrite a Draft changed since its run began: a stale result is rejected with an explanation. One-step Undo is available only while the current Draft still matches that successful turn's result, so it cannot erase intervening edits from another chat or a future editing surface.
