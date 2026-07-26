		if msg.KeyID != "" {
			flags |= flagHasKeyID
		}
		if msg.Payload != "" {
			// payload always included; no flag needed
		}
