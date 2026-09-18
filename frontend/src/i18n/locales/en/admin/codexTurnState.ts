export default { codexTurnState: {
  "title": "Codex renewal monitor",
  "description": "Manage accounts, models, proxy sources and renewal using the existing probe tool.",
  "accounts": "Manage accounts",
  "unavailable": "Panel operation failed. Check this project’s manager service and configuration.",
  "accountPicker": {
    "title": "Choose OpenAI OAuth-compatible account",
    "label": "Account",
    "placeholder": "Search active OpenAI OAuth-compatible accounts",
    "hint": "OAuth and setup-token accounts are eligible. Only the account name is shown here. Credentials remain in the existing account storage.",
    "empty": "No eligible active OpenAI OAuth-compatible accounts were found.",
    "loadError": "Unable to load eligible accounts. Search again or reopen this selector.",
    "incompatible": "unsupported name",
    "incompatibleHint": "Accounts with control characters or names longer than 128 characters cannot be used. Rename the account before selecting it.",
    "confirm": "Use account"
  }
} }
