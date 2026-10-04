# Let Builder runs continue after the browser leaves

An admitted Builder run in a Piko chat continues when its user navigates away, closes the chat, or closes the browser. Streaming displays progress, and the run's result is saved for the user to return to; an explicit user-operated Stop action cancels the work. We choose this over cancelling on browser disconnect because a dropped display connection should not discard requested work, accepting the need to track run ownership and status independently of that connection.

Stop prevents Draft changes that have not committed; already-applied changes remain and are subject to the Undo rules in ADR 0008. Deleting a Builder chat stops its active run while preserving previously applied Draft changes. Deploy is unavailable for a Bot while its Builder run is active, so the owner can inspect completed changes before making them live.

Server restarts mark unfinished runs as interrupted rather than automatically replaying model calls. Chat history and the last committed Draft remain available, and the owner can explicitly retry. This avoids repeating charges or assuming that an interrupted upstream request did not complete.

Initial defaults are 200 admitted requests per owner per day, 20 model calls per run, and an eight-minute run timeout, all configurable through environment variables. Failed admitted runs consume the daily request allowance; rejected requests do not. Usage and cost are recorded independently of optional Langfuse export so disabling or losing monitoring does not disable spending protection. These limits bound requests and run duration rather than promising a fixed monetary cap.

General Piko chats use the same durable run lifecycle and user-level accounting without requiring a Bot or a synthetic Draft. General conversations serialize active work per chat; once associated with a Bot, the existing per-Bot fence coordinates all of its Builder chats. Initial creation commits only on a successful validated outcome under ADR 0012, so Stop or failure before that commit leaves no Bot.
