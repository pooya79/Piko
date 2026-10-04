# Disconnect bot credentials separately from deleting bot data

Disconnecting a bot stops Piko's operation and removes its stored credentials while retaining configuration and submissions for viewing or reconnection. Replacing a token must preserve the same Telegram bot identity, and permanent deletion of the bot's Piko configuration and collected data is a separate explicitly confirmed action. This retains useful business records after a connection ends at the cost of storing disconnected bots; deleting a bot in Piko does not delete its Telegram identity in BotFather.

Bot deletion also deletes its associated Builder chats, including any general conversation that preceded the Bot's creation under ADR 0012. The deletion confirmation must explain that this earlier history is included. Piko chats that have never been associated with a Bot remain unaffected.
