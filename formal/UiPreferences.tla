---------------------------- MODULE UiPreferences ----------------------------
EXTENDS Naturals
Themes == {"Light", "Dark", "System"}
Dark(theme, system) == IF theme = "System" THEN system ELSE theme = "Dark"
VARIABLE u
Init == \E stored \in Themes \cup {"Invalid"}, system, plugin \in BOOLEAN:
  u = [theme |-> IF stored \in Themes THEN stored ELSE "System",
    system |-> system, dark |-> Dark(IF stored \in Themes THEN stored ELSE "System", system),
    page |-> "Home", autostart |-> FALSE, plugin |-> plugin,
    known |-> FALSE, pending |-> FALSE, previous |-> FALSE, requested |-> FALSE,
    error |-> FALSE, success |-> FALSE]
Theme(theme) ==
  /\ theme \in Themes
  /\ u' = [u EXCEPT !.theme = theme, !.dark = Dark(theme, u.system)]
SystemPreference(dark) ==
  /\ dark \in BOOLEAN
  /\ u' = [u EXCEPT !.system = dark,
    !.dark = IF u.theme = "System" THEN dark ELSE @]
Navigate(page) ==
  /\ page \in {"Home", "Settings", "History"}
  /\ u' = [u EXCEPT !.page = page]
ReadAutostart(ok) ==
  /\ ~u.known /\ ~u.pending /\ ok \in BOOLEAN
  /\ u' = [u EXCEPT !.known = ok, !.autostart = IF ok THEN u.plugin ELSE @]
ToggleAutostart ==
  /\ ~u.pending
  /\ u' = [u EXCEPT !.pending = TRUE, !.previous = u.autostart,
    !.requested = ~u.autostart, !.autostart = ~u.autostart, !.success = FALSE, !.error = FALSE]
ConfirmAutostart(persisted, readable) ==
  /\ u.pending /\ persisted \in BOOLEAN /\ readable \in BOOLEAN
  /\ LET confirmed == persisted /\ readable
     IN u' = [u EXCEPT !.pending = FALSE,
       !.plugin = IF persisted THEN u.requested ELSE @,
       !.autostart = IF confirmed THEN u.requested ELSE u.previous,
       !.known = confirmed, !.success = confirmed, !.error = ~confirmed]
Next == ToggleAutostart
  \/ (\E theme \in Themes: Theme(theme))
  \/ (\E dark \in BOOLEAN: SystemPreference(dark))
  \/ (\E page \in {"Home", "Settings", "History"}: Navigate(page))
  \/ (\E ok \in BOOLEAN: ReadAutostart(ok))
  \/ (\E persisted, readable \in BOOLEAN: ConfirmAutostart(persisted, readable))
Spec == Init /\ [][Next]_u
  /\ WF_u(\E persisted, readable \in BOOLEAN: ConfirmAutostart(persisted, readable))
ThemeResolvesCorrectly == u.dark = Dark(u.theme, u.system)
NoUnconfirmedAutostartSuccess == u.success => u.known /\ ~u.pending /\ ~u.error
ConfirmedAutostartMatchesPlugin == u.known /\ ~u.pending => u.autostart = u.plugin
ToggleEventuallySettles == u.pending ~> ~u.pending
=============================================================================
