package bot

// Publication and delivery are separate: a saved version alone is never active.
func overviewState(b Bot) string {
	if b.Unconnected {
		return "unconnected"
	}
	if b.Disconnected {
		return "disconnected"
	}
	if b.DeliveryState == "inactive" && b.PublishedVersion > 0 {
		return "published.inactive"
	}
	return b.DeliveryState
}

func overviewStatusKey(b Bot) string {
	if b.Unconnected {
		return "bot.status.unconnected"
	}
	if overviewState(b) == "published.inactive" {
		return "bot.overview.published.inactive"
	}
	return "delivery.state." + b.DeliveryState
}
