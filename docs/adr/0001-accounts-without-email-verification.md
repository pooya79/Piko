# Accounts without email verification

Piko uses email and password registration without an email ownership check. Email verification and password reset are outside the current product scope, so registration no longer depends on email delivery or Mailpit. Successful registration gives the user immediate access to their account.

An account's email address is a login identifier, not evidence of verified mailbox ownership. Existing accounts retain their credentials and can sign in regardless of their former verification status. Password recovery remains unavailable until a recovery flow is deliberately introduced.
