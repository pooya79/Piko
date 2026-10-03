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
A Telegram bot connected to Piko and operated on behalf of its Bot owner.
_Avoid_: Builder agent, chatbot

**Bot owner**:
The Piko user who connects and manages a Bot.
_Avoid_: Bot user, conversation participant

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

**Draft**:
A Bot's proposed Flow configuration that has not been published for live interactions.
_Avoid_: Published flow

**Preview**:
An isolated test of a Draft inside Piko that simulates a Participant's Telegram interaction.
_Avoid_: Live Telegram conversation, submission inbox

**Published flow**:
A Flow configuration approved by the Bot owner for live interactions.
_Avoid_: Draft

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
The planned AI agent that turns a Bot owner's request into a Flow assembled from approved Blocks.
_Avoid_: Bot, chatbot, AI reply block

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
