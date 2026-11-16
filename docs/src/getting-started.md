# Getting started

## Start the server

Follow the product reference for the published-image or source-build installation. Keep the selected Compose overlays and persistent database. Open the configured origin, sign in with the bootstrap administrator password and replace it when prompted.

## Prepare accounts

Use People for local everyday accounts and Groups for local membership. Sign-in settings configure KyIdentity or another OIDC provider. Use a dedicated administrator identity for management and an everyday identity for calendars: administrators cannot use CalDAV or create app passwords.

## Connect a calendar client

With your everyday account, create a per-device app password. Configure the client's CalDAV account with the server's HTTPS origin, your username and that app password. Discovery uses `/.well-known/caldav`; a browser session cookie is not a CalDAV credential. Create a test event and check that it appears in both the web app and the client.
