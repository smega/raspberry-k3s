# NewVersionLive

**Severity:** notice ( a low-priority phone notification, no action needed )

## What fired

A Flagger canary was promoted: a new version of the status page or the
status API passed its checks and now serves all traffic. The message names
the app and the commit, and links the commit on GitHub.

## What it means for visitors

They are on the new version. Nothing is wrong; this is the "it's out"
message, so a release isn't something you have to go and look for.

## How to check

- `kubectl -n public-demo get canary`: Succeeded, weight 0.
- The Cluster tab's "How often it ships" counts it as a deploy.

## If it never comes

A release that fails sends CanaryRolledBack instead, and one that hangs
sends CanaryStuck after 20 minutes. If neither and no NewVersionLive came
after a push, the image or its tag never reached the cluster: look at the
CI run and the ImageUpdateAutomation.

## History

- New on 2026-11-26, after two releases that morning failed quietly on a
  node with a slow SD card ( CanaryRolledBack did fire for both ). The
  next one, the power charts ( c9d7bc1 ), was the first a backtest of this
  rule would have announced.
