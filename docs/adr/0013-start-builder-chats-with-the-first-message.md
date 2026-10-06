# Open the Bot studio with an unsaved Builder conversation

Entering a Bot studio opens a fresh conversation beside its Preview, Flow canvas, and Draft changes; existing Builder chats remain available in the sidebar. We remove the separate chat-list and title-entry page to avoid an extra step before working on a Bot, choosing a fresh composer over automatically resuming a previous conversation.

A new Builder chat is saved only when its first message is accepted, with its title derived from that message. Chat creation, the first message, and Builder run admission commit together: validation, busy, allowance, or storage failures leave no empty saved chat. Durable request deduplication makes a repeated first submission resolve the same chat and run. After admission, failures or Stop retain the chat and its accepted message; ADR 0008's separate conversation memory and shared Bot Draft remain unchanged.
