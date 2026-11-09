import { basePath } from './paths'

// The dev server is Vite, which is not mounted on either prefix. The
// public index talks to /s and the console talks to /a, the defaults.
// A configured prefix is what the page was actually served on, which
// basePath already is outside of dev.
const devServer = 'http://localhost:9150'
const apiBase = (devMode, isAdmin) =>
  devMode ? `${devServer}${isAdmin ? '/a' : '/s'}` : basePath()

const post = async (body) => {
  // Mutations belong to the console. In dev the page is not served on
  // its prefix, so the call goes to the console's default mount.
  const dev = document.getElementById('root')?.getAttribute('dev-mode') === 'true'
  const resp = await fetch((dev ? `${devServer}/a` : basePath()) + '/', {
    method: 'POST',
    headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  // A message is a refusal, whatever the status. A refusal has arrived
  // as 200, and taking that for a success told the operator a link was
  // created when it was not. An alias with no message is the link a
  // create stored, including one the server chose.
  let payload = {}
  try {
    payload = (await resp.json()) || {}
  } catch {
    // No body, or not JSON: nothing was said.
  }
  if (payload.message) {
    return { error: payload.message, alias: null }
  }
  if (!resp.ok) {
    return { error: `request failed (${resp.status})`, alias: null }
  }
  return { error: null, alias: payload.alias || null }
}

const get = async (devMode, isAdmin, params) => {
  const resp = await fetch(`${apiBase(devMode, isAdmin)}/?${new URLSearchParams(params)}`)
  if (!resp.ok) {
    throw new Error(`request failed (${resp.status})`)
  }
  return resp.json()
}

// fetchIndex returns a page of links. The admin listing carries the
// counts and a series per row; the public one carries neither.
export const fetchIndex = async ({ isAdmin, devMode, page, pageSize }) => {
  const data = await get(devMode, isAdmin, {
    mode: isAdmin ? 'index-pro' : 'index',
    pn: page,
    ps: pageSize,
  })
  const rows = (data.data ?? []).map((r) => ({
    ...r,
    // The zero time means "valid since always". Carrying it into a date
    // field would render the year 1 and offer to edit it.
    valid_from: r.valid_from === '0001-01-01T00:00:00Z' ? null : r.valid_from,
  }))
  return { ...data, data: rows }
}

export const fetchOverview = (devMode, t0, t1) =>
  get(devMode, true, { mode: 'overview', t0, t1 })

export const fetchStat = (devMode, alias, stat, t0, t1) =>
  get(devMode, true, { mode: 'stats', a: alias, stat, t0, t1 })

export const save = (op, alias, data) => post({ op, alias, data })

export const remove = async (alias) =>
  (await post({ op: 'delete', alias })).error
