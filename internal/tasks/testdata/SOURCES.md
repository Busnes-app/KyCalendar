# Fixture sources

- `apple_*.ics`, `thunderbird_*.ics`: copied unchanged from Tasks.org's test resources
  (`app/src/test/resources/apple/` and `.../thunderbird/`, github.com/tasks/tasks at
  `8f842215841d953d05727ce53dfb6e2507b8bfee`, GPL-3.0 test data), which Tasks.org captured
  from Apple Reminders (iOS 12) and Thunderbird. `apple_` and `thunderbird_` prefixes added.
- `tasksorg_subtask.ics`: written for this repo with the properties Tasks.org's CalDAV writer
  emits (`kmp/src/commonMain/kotlin/org/tasks/caldav/iCalendar.kt` at the same commit:
  `X-APPLE-SORT-ORDER`, `X-OC-HIDESUBTASKS`, `X-MOZ-SNOOZE-TIME`, `RELATED-TO;RELTYPE=PARENT`,
  its PRODID form), with fake data.
