import { basePath } from './paths'

// origin is where the API lives. In development the dashboard is served
// by Vite and the server is elsewhere, so the two are not the same.
const origin = (devMode) =>
  devMode ? 'http://localhost:9150/s' : basePath()

const post = async (body) => {
  const resp = await fetch(basePath() + '/', {
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

const get = async (devMode, params) => {
  const resp = await fetch(`${origin(devMode)}/?${new URLSearchParams(params)}`)
  if (!resp.ok) {
    throw new Error(`request failed (${resp.status})`)
  }
  return resp.json()
}

// fetchIndex returns a page of links. The admin listing carries the
// counts and a series per row; the public one carries neither.
export const fetchIndex = async ({ isAdmin, devMode, page, pageSize }) => {
  const data = await get(devMode, {
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
  get(devMode, { mode: 'overview', t0, t1 })

export const fetchStat = (devMode, alias, stat, t0, t1) =>
  get(devMode, { mode: 'stats', a: alias, stat, t0, t1 })

export const save = (op, alias, data) => post({ op, alias, data })

export const remove = async (alias) =>
  (await post({ op: 'delete', alias })).error
