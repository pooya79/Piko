# Piko

Piko is a workspace for building and maintaining Telegram business bots.

## Language

**Display name**:
The name a user supplies to identify themselves within Piko, including their dashboard greeting and profile.
_Avoid_: Legal name, email address

**Dashboard**:
The signed-in user's overview of their bots, conversations, and recent activity.
_Avoid_: Home, account page

**Bot**:
A Telegram bot configuration managed in Piko on behalf of its Bot owner. It can be built before connecting a Telegram identity and credentials.
_Avoid_: Builder agent, chatbot

**Bot owner**:
The Piko user who creates and manages a Bot, including its Telegram connection.
_Avoid_: Bot user, conversation participant

**Bot studio**:
The Bot owner's workspace for building and maintaining one Bot, combining Builder chats with its Flow canvas, Preview, and Draft changes.
_Avoid_: Dashboard, visual block editor, Builder chat

**Unconnected Bot**:
A Bot that has not yet been connected to a Telegram identity and can be built and previewed inside Piko.
_Avoid_: Disconnected Bot

**Disconnected Bot**:
A previously connected Bot whose Telegram credentials have been removed while its identity and retained data remain in Piko.
_Avoid_: Unconnected Bot, deleted Bot

**Participant**:
A person interacting with a Bot in Telegram.
_Avoid_: Bot owner, Piko user

**Block**:
A reusable, approved unit of bot behavior, such as showing buttons or collecting an answer.
_Avoid_: Template, generated script

**Question block**:
A Block that asks a Participant for one answer of a specified type, such as text, a phone number, or a date.
_Avoid_: Web form, form builder

**Form**:
A configured collection of Question blocks through which a Participant provides answers for a Submission inside Telegram.
_Avoid_: Web form, bot builder

**Flow**:
An arrangement of Blocks that defines a Bot's behavior and the paths an interaction can take.
_Avoid_: Template, agent

**Flow canvas**:
A visual map of a Bot's Flow whose Blocks the Bot owner can inspect and reference when requesting changes from Piko.
_Avoid_: Visual block editor, Draft, Preview

**Draft**:
A Bot's single editable Flow configuration whose changes have not been published for live interactions. Its Builder chats work on the same Draft.
_Avoid_: Published flow

**Preview**:
An isolated test of a Draft inside Piko that simulates a Participant's Telegram interaction.
_Avoid_: Live Telegram conversation, submission inbox

**Published flow**:
A Flow configuration approved by the Bot owner for live interactions.
_Avoid_: Draft

**Deployment**:
A Bot owner's explicit action to publish a Draft and activate the Bot's Telegram delivery.
_Avoid_: Draft save, Telegram connection

**Interaction**:
A Participant's attempt to complete a Form, including their unfinished answers and progress.
_Avoid_: Submission, Telegram chat

**Flow version**:
A particular published revision of a Flow that an Interaction follows from start to finish.
_Avoid_: Draft, latest configuration

**Template**:
A ready-made Flow that a Bot owner can configure for a particular use case.
_Avoid_: Block

**Builder agent**:
The AI assistant called Piko that answers questions about Piko and creates and updates Drafts from a Bot owner's requests using approved Blocks. The Bot owner decides when its changes become live in Telegram.
_Avoid_: Bot, chatbot, AI reply block

**Builder chat**:
A Piko chat associated with one Bot. A Bot can have multiple Builder chats, each with its own conversation memory.
_Avoid_: Participant conversation, Telegram chat

**Piko chat**:
A Piko user's saved conversation with the Builder agent about the application or a proposed Bot. After a successful request to create a Bot, the same conversation becomes that Bot's Builder chat.
_Avoid_: Bot, Participant conversation, Telegram chat

**Builder run**:
The Builder agent's work on one request in a Piko chat, which may produce a reply, an initial Bot, or Draft changes. It continues when the user leaves or closes the chat, unless the user stops it.
_Avoid_: Participant interaction, Telegram delivery

**Inquiry**:
A request containing contact details and a message for a business to review.
_Avoid_: Booking request, registration

**Registration**:
An application to an event or service collected by a Bot, without a guarantee of acceptance or capacity.
_Avoid_: Piko account registration, booking request

**Booking request**:
A request containing a preferred date and booking details for a business to review.
_Avoid_: Confirmed booking, reservation

**Submission**:
A set of answers explicitly confirmed by a Participant and collected by a Bot for its Bot owner to review in Piko.
_Avoid_: Conversation, unfinished answers
