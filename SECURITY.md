# Security policy

Report a vulnerability in clued (the plugin, the `mind-palace` binary and
module, the daemon or the self-hosted clued server) or in Clued Cloud
privately by e-mail to **security@clued.cloud**. Never report one in a
public issue, pull request or discussion.

Include what is affected (version: `mind-palace version`, or the
`/health` of a server), how to reproduce it, and what an attacker gains.
Do not send real tokens, session data or other people's information; a
redacted excerpt is enough.

You get an acknowledgement within three working days. A fix ships in the
next release (every push to main is a release, see the README), and the
advisory credits you unless you ask otherwise.

Supported versions: the latest release. The plugin downloads the binary of
the release it pins, so updating the plugin updates the binary.
