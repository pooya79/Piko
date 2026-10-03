# Disconnect bot credentials separately from deleting bot data

Disconnecting a bot stops Piko's operation and removes its stored credentials while retaining configuration and submissions for viewing or reconnection. Replacing a token must preserve the same Telegram bot identity, and permanent deletion of the bot's Piko configuration and collected data is a separate explicitly confirmed action. This retains useful business records after a connection ends at the cost of storing disconnected bots; deleting a bot in Piko does not delete its Telegram identity in BotFather.
