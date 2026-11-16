# TurtleRoute help center

Static source for the public `/helpcenter` route and its console embed. The
[AWS candidate runbook](../../../AWS_CANDIDATE_20260923.md) and
[WWW origin recovery record](../../../../docs/operations/WWW_ORIGIN_RECOVERY_20260912.md)
document the Caddy route and its separate static volume. This directory is a
source snapshot; committing it does not publish it or change the console build.

The route serves `help-center.html` at `/helpcenter`; its child paths serve the
versioned assets at their paths relative to this directory. The v5 style builds
on v4 and reuses the v3 interaction script and the v2 mark and tutorial images.
The captures use demonstration data; the steps appear as small numbers beside
the relevant controls. The console supplies the header when the guide is embedded.

For a static release, publish new versioned assets before switching the HTML.
Keep existing version directories and the previous HTML available for rollback.
