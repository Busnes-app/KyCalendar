# Troubleshooting

- **CalDAV login fails:** use an everyday account and its per-device app password, not a browser cookie or an administrator account.
- **A shared calendar is absent:** ask the administrator to check group membership and its reader/editor/manager grants.
- **A change conflicts:** reload the current event before applying your changes again.
- **A recurrence is marked partial:** the server bounded its expansion; inspect the series instead of assuming all occurrences are shown.
- **New administrator has no access:** KyIdentity administration requires the explicit `kycalendar.admin` app role; a global role does not grant it.
- **Backup fails:** check the pinned key, configured destination and last attempt; a local directory on the same host does not provide an independent failure domain.

When reporting a problem, include the product version, platform, failed task and a redacted error. Exclude pairing links, device secrets, recovery codes, custodian shares and private content.
