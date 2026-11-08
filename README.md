# gore
[![Latest release](https://img.shields.io/github/v/tag/rukins/gore?label=latest)](https://github.com/rukins/gore/releases)

A self-hosted link shortener and Go vanity import server, in one binary.

You own the domain, the links and the numbers. `rukins.dev/s/blog`
redirects; `rukins.dev/x/gore` resolves for `go get`; the console shows
who followed what, with crawlers separated out rather than counted as
readers.

![The gore operator console](./.github/assets/admin.png)

## What it does
- **Short links** under `/s/`, with semantic aliases rather than
  generated ones: `/s/blog`, `/s/talks/2026`.
- **Go vanity imports** under `/x/`, serving the `go-import` meta tag so
  `go get yourdomain.com/x/repo` resolves to your VCS.
- **A public index** of the links a visitor may follow, which discloses
  neither targets nor traffic.
- **An operator console** at `/s?mode=admin`: totals, the month as a
  chart, a dense listing with recent traffic per link, and per-link
  detail. It follows the system's light or dark setting.
- **Statistics** of page and unique views over time, referring hosts,
  browsers, systems and devices. Automated traffic is classified where a
  visit is recorded and excluded from every figure, with a count of what
  was left out. On a real deployment that is around half of all visits.
- **Several sites from one process**, each with its own aliases and its
  own statistics.
- **Access control** per link: keep it off the public index, hold it
  until a date, or warn before sending someone off-site.
- **Pages the GDPR asks for**: impressum, privacy and contact, with
  optional IP hashing.

| Public index | Wating until a date | Leaving the site |
|:---:|:---:|:---:|
| ![](./.github/assets/index.png) | ![](./.github/assets/waiting.png) | ![](./.github/assets/warning.png) |

## Getting started

gore stores links and visits in a SQLite file by default. The file and its directory are created on startup. PostgreSQL is also supported.

Copy the default configuration. It already names the file:

```sh
$ mkdir -p data && cp internal/config/config.yml data/goreconf.yml
```

```yaml
host: https://example.com
addr: :80
store: sqlite://data/gore.db
```

To use PostgreSQL instead, create a database and a role and point `store` at them:

```sql
CREATE ROLE gore LOGIN PASSWORD 'secret';
CREATE DATABASE gore OWNER gore;
```

```yaml
store: postgres://gore:secret@localhost:5432/gore?sslmode=disable
```

Then run it, with Docker. Compose keeps `data/` on the host, so the SQLite file survives a recreate, and it does not start PostgreSQL:

```sh
$ docker network create traefik_proxy
$ make build && make up
```

or from source, which needs [Go](https://go.dev) 1.27 and
[Node.js](https://nodejs.org) 22:

```sh
$ make dashboard   # build the front end
$ make             # build the server, embedding it
$ GORE_CONF=data/goreconf.yml ./gore -s
```

The schema is applied at start up from migrations embedded in the binary,
so there is nothing to run first. Each start applies what is missing and
nothing else.

`data/goreconf.yml` is not tracked and is mounted into the container
rather than built into the image, because it holds credentials. If
`GORE_CONF` names a file that cannot be read the server stops, rather
than starting quietly against the wrong database.

## Configuration

### Signing in

`auth.enable` decides who may administer the links:

| Mode | Who can administer |
|---|---|
| `latere` | Anyone signing in through [auth.latere.ai](https://auth.latere.ai) who is on the allowlist |
| `basic` | The username and password pairs under `auth.basic` |
| `none` | Everyone. Only sensible behind other access control |

`latere` runs OAuth 2.0 with PKCE and keeps the result in an encrypted
session cookie. Register an OAuth client whose `redirect_uris` include
`<host>/s/.auth/callback` and whose `allowed_origins` include `<host>`,
for each site you serve. Set the environment variables in
[.env.template](./.env.template).

Signing in proves who someone is, not that they may manage your links, so
`AUTH_ALLOWED_PRINCIPALS` decides that separately: a comma separated list
of emails or principal ids, matched case insensitively. An empty list
rejects every login.

### Several sites
The store keys links by hostname, so each site has its own aliases and
its own statistics, and a request belongs to whichever site its `Host`
header names. Only what differs needs configuring:

```yaml
host: https://example.com
x:
  repo_path: https://github.com/example
hosts:
  examplepkg.com:
    # /x/ import paths and the VCS probe resolve here instead.
    repo_path: https://github.com/examplepkg
    # The impressum and privacy pages name an operator, so they do not
    # follow onto another domain unless it is the same one.
    legal: false
```

A `Host` header naming no configured site falls back to the primary one.
It is chosen by the client and becomes part of a link's identity, so it
is matched against the configured sites rather than trusted.

## Command line

The server is `gore -s`. The same binary manages links from a shell,
which is how a deployment without a browser adds one:

```sh
$ gore -op create -a blog -l https://blog.example.com -trust
$ gore -l https://blog.example.com   # alias is chosen for you
$ gore -op update -a blog -l https://blog.example.com/new
$ gore -op fetch  -a blog
$ gore -op delete -a blog

$ gore -f import.yml   # import aliases from a file
$ gore -d export.yml   # dump them to one
```

| Flag | Meaning |
|---|---|
| `-p` | private: the link works but is not listed publicly |
| `-trust` | redirect directly instead of warning before leaving the site |
| `-vt` | hold the link until an RFC 3339 time |
| `-rederive` | recompute the stored browser, system, device and bot columns, then exit. Needed after a change to how visits are classified |

## Documentation
- [API reference](./docs/api.md)
- [GDPR](./docs/gdpr.md)

## Contributing
This is an open-source project and all contributions are highly welcomed

## License
This project is based on [redir](https://github.com/changkun/redir) by Changkun Ou, licensed under the MIT License.

Original code: MIT License &copy; 2020-2026 [Changkun Ou](https://changkun.de)
Modifications: MIT License &copy; 2026 [Sergey Rukin](https://rukins.dev)
