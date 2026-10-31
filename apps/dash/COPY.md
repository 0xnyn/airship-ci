# Dashboard copy

The dashboard is not the documentation. `apps/docs` exists to explain Airship
at length, and it is good at it; a dashboard that re-explains the product in
its own subtitles makes the user read the same paragraph every time they
navigate. Every line here competes with the data the page is actually for.

Rules below follow [Grafana's UX writing style
guide](https://grafana.com/docs/writers-toolkit/write/style-guide/ux-writing/)
— the closest published guide to this kind of product — with empty states from
[NN/g](https://www.nngroup.com/videos/empty-states-in-application-design-guidelines/),
[PatternFly](https://v5-archive.patternfly.org/components/empty-state/design-guidelines)
and [SAP Fiori](https://www.sap.com/design-system/fiori-design-web/v1-96/foundations/best-practices/global-patterns/designing-for-empty-states).

## The rules

**Length.** A page description is one line: what is on this page, at most
about twelve words. Help text is one or two short sentences. If a third
sentence wants in, it is documentation — link to it.

**Say what it is, not how it works.** "Where Airship launches runners", not
"Airship launches every runner in your own cloud account with the credentials
you connect here." Mechanism, trust rules and cloud setup belong in
`apps/docs`.

**Cut the rationale.** Help text states constraints and consequences, not why
the feature is good for you. "Jobs past the limit wait for a slot" earns its
place; "it bounds what a config file can cost you" does not.

**Don't narrate what is already on screen.** The commonest failure is a
subtitle that inventories the page's own columns and controls, joined on with
a comma and an "and". "Every workflow job GitHub reported, **and which runner
served it**" — that trailing clause is the Runs-on column. "Who can open this
dashboard, **and what they can do**" is the Role column. "Machines Airship
launched in your cloud, **live and finished**" is the filter tabs. Say the one
thing the page cannot show, or stop at the first clause. One sentence, no
connective, about eight words.

**No navigation instructions.** The user knows how to come back. "Install it
on yours, **then come back here**" spends a clause on the browser. The empty
install screen now reads "Airship only sees organizations it is installed on"
— which explains why the screen is empty, the one thing a heading and a button
can't.

**Link instead of explaining.** `DocsLink` (`components/docs-link.tsx`) takes
a page from `lib/docs.ts`, and the key *is* the link text, so a link always
names its destination the way the documentation titles it. Write the complete
UI text first and link only for the extra detail.

**Punctuation.** Sentence case everywhere. No full stop on a single-sentence
description, empty state, label or help line; use full stops once there are
two sentences. No colons after labels.

**Buttons name their action.** A verb, one or two words. "Connect", not
"Save", when the thing being done is connecting a cloud. Never "OK", and never
"Open" where the destination can be named.

**Empty states** say what is missing and the one next thing to do, in at most
two short sentences. "No active runners. One launches as soon as a job is
queued for an `airship-` label." Not a tutorial.

**Errors** say what failed and what to do. No blame, no apology, no "Oops". A
cloud's own message is better than a paraphrase of it.

**One name per concept, everywhere.** The queue measure is "Median queue
time" on every page. A runner is `Waiting / Launching / Idle / Busy` in the
overview grid and in the runners table alike. The API keys page says "key" in
its errors and its table headers, not "token". Pick the word once, then grep
before you introduce a second one.

## Before and after

| | Before | After |
| --- | --- | --- |
| Cloud | "Airship launches every runner in your own cloud account with the credentials you connect here. They are stored encrypted and never shown again. Connect any of the clouds; each runner definition says which it launches in." (33w) | "Where Airship launches runners. Credentials are encrypted and never shown again." (11w) |
| Runners | 34 words on what a runner is and when one starts | "Machines Airship launched in your cloud" (6w) |
| Teams | 32 words restating the whole GitHub access model | "Who can open this dashboard" (5w) |
| API keys | 29 words, including `provision_via: pull_from_api` | "Tokens for scripts and the Airship API" (7w) |
| Azure setup | 110-word paragraph on resource groups, custom roles, marketplace image terms and quota | two sentences plus a link to **Microsoft Azure** |
| Install screen | "Airship runs workflows for organizations that installed its GitHub App. Install it on yours, then come back here." | "Airship only sees organizations it is installed on." |
