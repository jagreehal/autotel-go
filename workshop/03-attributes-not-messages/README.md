# Part 3: Attributes, not messages

## The scene

The bank runs three ATMs: two NCR-6622s and one Diebold-522, spread across two
branches. Support says some withdrawals feel slow and asks one question:

> Is it one machine, one model, or one branch?

You have traces now. Each one is called `atm.withdraw`, and most take about 75ms.
The facts that would answer support sit inside English log messages:

```go
fmt.Printf("withdrawal at %s (%s)\n", m.id, m.model)
fmt.Printf("customer %s\n", user.Name)
```

Devtools cannot search those strings, so the withdrawals all look the same.

```sh
go run ./workshop/03-attributes-not-messages/exercise
```

## Predict, before you run it

> `atm.model` takes two values across the estate. `withdrawal.transaction_id`
> differs on each withdrawal ever made. Which of the two is more useful to record,
> and which one would a metrics system (a tool such as Prometheus that keeps
> running counts) charge you for?

## Your turn

Move the facts out of the strings and onto the span as attributes. An
**attribute** is a named value attached to a span, such as
`atm.branch = "bridge-street"`. Devtools can search attributes, and it cannot
search a message you printed to stdout.

Then answer these three from the devtools search box, without opening the source:

1. How many withdrawals happened at `bridge-street`?
2. Did any withdrawal take longer than 100ms, and on which machine?
3. What did user `u_007` do?

`autotel.SetAttribute` and `autotel.SetAttributes` write to the span in a `ctx`.

## Our solution

Click **Clear** in devtools, then run:

```sh
go run ./workshop/03-attributes-not-messages/solution
```

```go
autotel.SetAttributes(ctx, map[string]any{
	"atm.id":                  m.id,
	"atm.model":               m.model,
	"atm.branch":              m.branch,
	"withdrawal.amount_pence": amountPence,
})

user, err := bank.FindUserByCard(cardNumber)
// ...
autotel.SetAttribute(ctx, "user.id", user.ID)
```

`SetAttribute` takes a string, an integer, a float or a bool and stores it with its
type. The `autotel.Span` that `autotel.Trace` hands your function has the same
method, for when you already hold it.

Further down, the solution adds `account.id`, `account.balance_pence` and
`withdrawal.transaction_id` the same way. Type these into the search box, one at a
time:

```
atm.branch = "bridge-street"
name = "atm.withdraw" and duration > 100
user.id = "u_007"
```

`duration` is in milliseconds. The first query returns six withdrawals, and the
heading reads `Traces (6 of 9)`. The second returns one. Click its row, then click
`atm.withdraw` in the waterfall, and read the Attributes panel: `atm.id` is
`atm_0431` and `user.id` is `u_007`, the same withdrawal the third query finds. That
withdrawal took about 355ms while the other eight took about 75ms. Part 10 explains
why.

```mermaid
flowchart LR
  subgraph before["Exercise: facts in strings"]
    A["fmt.Printf(#quot;withdrawal at atm_0431 (NCR-6622)#quot;)"] --> B["stdout: one line of English"]
    B --> C["grep or a regex, per question"]
  end
  subgraph after["Solution: facts on the span"]
    D["autotel.SetAttribute(ctx, #quot;atm.id#quot;, m.id)"] --> E["atm.withdraw span<br/>atm.id = atm_0431<br/>atm.model = NCR-6622<br/>atm.branch = bridge-street"]
    E --> F["devtools query<br/>atm.branch = #quot;bridge-street#quot;"]
  end
```

### Why this solution

**The span travels in `ctx`.** You reach it from anywhere you pass `ctx`, and unlike
a global, each concurrent call sees its own value. It resolves to whichever span
`ctx` holds, including several calls deep inside a helper. Part 2's badge provides
it: each concurrent call to `withdraw` holds its own `ctx`, so nine calls running at
once write to nine separate spans. Outside any span, `autotel.SetAttribute` does
nothing, so a `ctx` that lost its badge drops your attributes without a word. If a
field you set does not appear, check which `ctx` you passed.

```mermaid
flowchart TD
  C1["withdraw() for u_001"] --> S1["ctx holds atm.withdraw #1"]
  C2["withdraw() for u_007"] --> S2["ctx holds atm.withdraw #7"]
  S1 --> W1["SetAttribute(ctx, ...) writes to span #1"]
  S2 --> W2["SetAttribute(ctx, ...) writes to span #7"]
```

**A message is a string; an attribute is a column.** Picture the spans from this run
as rows in a spreadsheet, one row per withdrawal and one column per attribute:

| span           | atm.id     | atm.model     | atm.branch      | user.id  | duration |
| -------------- | ---------- | ------------- | --------------- | -------- | -------- |
| `atm.withdraw` | `atm_0431` | `NCR-6622`    | `bridge-street` | `u_001`  | ~75ms    |
| `atm.withdraw` | `atm_0912` | `NCR-6622`    | `kings-cross`   | `u_002`  | ~75ms    |
| `atm.withdraw` | `atm_1180` | `Diebold-522` | `bridge-street` | `u_003`  | ~75ms    |
| ...            |            |               |                 |          |          |
| `atm.withdraw` | `atm_0431` | `NCR-6622`    | `bridge-street` | `u_007`  | ~355ms   |

`user.id = "u_007"` filters on one column, like a spreadsheet filter.
`"customer Ada Okafor"` needs a regular expression over English, and your expression
breaks the day a colleague rewords the message. _Observability Engineering_
(Chapter 5) makes the same move with web-server logs: once `user` is a field, "count
unique users between two time periods" becomes a `SELECT DISTINCT user` instead of a
regular expression run over each message.

**Record high-cardinality fields.** **Cardinality** is how many different answers a
field can have. Ask "which model?" and you get one of two answers. Ask "which
transaction?" and each withdrawal gives a new answer, millions over a year. Years of
metrics bills taught engineers to fear the second kind, and the book argues for it:

> High-cardinality data is the most descriptive and identifying type of data,
> and it is vital for observability. Think of all the most useful attributes you
> would like to search or group by: build number, commit hash, container ID,
> span ID, user ID, app name, query hash, etc. All of which are high cardinality,
> and that is why they're useful.

Chapter 1, the source of that passage, also names a second measure.
**Dimensionality** is how many fields one record carries. Cardinality counts the
answers to one question, and dimensionality counts the questions one record can
answer. This part adds dimensions to `atm.withdraw`, and part 5 shows how each new
one multiplies what you can ask.

The fear comes from how metrics work. A **metric** is a named number the system
keeps updating, such as `withdrawals_total`. Give it a `branch` label and the system
keeps a separate running number per branch: one for bridge-street, one for
kings-cross. Each of those running numbers, recorded minute by minute, is a **time
series**. Label the metric by `transaction_id` and the system starts a new series for
each withdrawal, and metrics tools store, and often bill, per series. A trace backend
stores the ID as one more field on one span and creates no series.

**A metric decides the question before you ask it.** A metric works like a tally
counter at a door: it tells you six people came in, and never who they were. Span
attributes work like a pile of receipts you can sort any way you like. Suppose you
had counted withdrawals as a metric labelled by branch. The metrics system adds the
nine withdrawals into two numbers, six at bridge-street and three at kings-cross, and
discards the rows. Then support asks about the model. The metric has no model label,
and no query can split a sum back into the rows that made it. The book calls this
"Pre-aggregation is a one-way trip" (Chapter 1). In part 9 you build a metric like
this one and see the jobs it does better than spans.

```mermaid
flowchart LR
  W["9 withdrawals"] --> M["metric withdrawals_total by branch<br/>bridge-street: 6<br/>kings-cross: 3"]
  W --> S["9 atm.withdraw spans<br/>atm.id, atm.model, atm.branch, user.id"]
  M --> Q1["By model? No label, no answer"]
  S --> Q2["Filter by atm.model, atm.id or user.id<br/>chosen at query time"]
```

That answers the prediction: record both. `atm.model` sorts withdrawals into two
groups, `withdrawal.transaction_id` finds one withdrawal among millions, and only the
metrics system would charge you for the second. Keep IDs out of attribute _names_,
such as `latency.u_007`, where each new user creates a new column.

**Follow a naming convention.** Name attributes in lowercase, dotted, and namespaced
by the thing they describe: `atm.id`, `user.id`, `withdrawal.amount_pence`.
OpenTelemetry publishes **semantic conventions**, a shared dictionary of attribute
names such as `http.request.method`, `db.system.name` and `server.address`. Tools
that read OpenTelemetry agree that `http.request.method` holds the HTTP verb, so they
can build views on it with no setup from you. Use those names where they fit. Invent
names for your own domain and keep them consistent: devtools cannot treat `atm.id`
from one service and `atmId` from another as the same field.

Span names follow the same rule: name the operation, `atm.withdraw`, and move the
variable parts into attributes. A span called `withdraw u_007` gives each customer
their own name, so you can no longer group by name. The book's example rewrites
`GET /users/profile/349827487124` as `GET /users/profile/{user.id}` (Chapter 7). Put
the unit in the key, as `withdrawal.amount_pence` does, and keep one unit per key. A
field that holds pence on one span and pounds on another corrupts any sum you take
over it.

### What to record

Ask what you would need during an incident. These categories cover most of it:

| Category   | Examples                                                    |
| ---------- | ----------------------------------------------------------- |
| Who        | `user.id`, `account.id`, `tenant.id`                        |
| Where      | `atm.id`, `atm.branch`, `host.name`, `deployment.environment.name` |
| What       | `withdrawal.amount_pence`, `http.route`, `db.operation.name` |
| Outcome    | `withdrawal.result`, `error.type`, `http.response.status_code` |
| Which code | `service.version`, commit sha, feature flag values          |

`autotel.Init` attaches some of these for you as **resource attributes**, facts about
the process, not the request. Click any span in devtools and you will find
`service.name`, `service.version` and `deployment.environment` in its Attributes
panel. Set them with `autotel.WithServiceVersion` and `autotel.WithEnvironment` when
you deploy, or through `OTEL_RESOURCE_ATTRIBUTES`, which also takes a commit sha or
`host.name`, so you can compare one release, or one machine, against the next.

Automatic instrumentation fills in the HTTP and database rows for you. It cannot know
that your bank has branches, so you add the attributes specific to your application.
The book rates those as the most valuable (Chapter 6).

Leave out what you would have to protect. The solution records `user.id` and drops
`user.name`: the ID finds the customer, and the name is personal data that each tool
storing your telemetry would then hold.

---

Previous: [Part 2: Your first span](../02-your-first-span/README.md) ·
Next: [Part 4: Which work deserves a span](../04-which-work-deserves-a-span/README.md)
