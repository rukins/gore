const strip = (p) => (p.endsWith('/') ? p.slice(0, -1) : p)

// basePath returns the path this page is mounted on, without its trailing
// slash. The public index is "/s"; the console is whatever prefix serves
// it, so the API follows the page and a proxy can match one prefix.
export const basePath = () => strip(window.location.pathname)

// shortBase is the shortener, not this page. The console is served on its
// own prefix, and a link copied out of it is still a short link.
export const shortBase = () => {
  const raw = document.getElementById('root')?.getAttribute('short-prefix') || ''
  if (raw.startsWith('/')) return strip(raw)
  return basePath()
}

// aliasPath returns the server-relative short link for an alias.
export const aliasPath = (alias) => `${shortBase()}/${alias}`

// aliasURL returns the absolute short link for an alias, which is what a
// visitor needs when the link is copied out of the dashboard.
export const aliasURL = (alias) =>
  `${window.location.origin}${aliasPath(alias)}`
