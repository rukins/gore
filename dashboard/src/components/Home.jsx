import { useCallback, useEffect, useState } from 'react'
import Shell from './Shell'
import Overview from './Overview'
import LinkTable from './LinkTable'
import LinkForm from './LinkForm'
import { fetchOverview } from '../lib/api'
import { defaultRange } from '../lib/time'

// overviewDays is how far back the totals and the chart reach, which is
// what defaultRange covers. Thirty days is long enough for a weekly rhythm
// to be visible without flattening what happened this week.
const overviewDays = 30

const Home = (props) => {
  const [overview, setOverview] = useState(null)
  const [reloadKey, setReloadKey] = useState(0)
  const [creating, setCreating] = useState(false)
  // What the last action did, said once beside the list it changed. An
  // error stays until the next action; the rest is not worth a dialog.
  const [status, setStatus] = useState({ text: '', error: false })
  const notify = useCallback(
    (text, error = false) => setStatus({ text, error }),
    [],
  )

  const loadOverview = useCallback(async () => {
    if (!props.isAdmin) return
    // The range ends tomorrow, as a link's own does: the server reads the
    // end as that day's midnight, so ending today left today out.
    const [start, end] = defaultRange()
    try {
      setOverview(await fetchOverview(props.devMode, start, end))
    } catch (e) {
      notify(`Could not load the totals: ${e.message ?? e}`, true)
    }
  }, [props.isAdmin, props.devMode, notify])

  useEffect(() => {
    loadOverview()
  }, [loadOverview, reloadKey])

  const refresh = () => setReloadKey((k) => k + 1)

  // Legal pages stay on the shortener. A relative link from the console
  // would look for them under the console prefix, where they are not.
  const legal = (name) => {
    const base = (props.shortPrefix || '').replace(/\/$/, '')
    return base ? `${base}/.${name}` : `./.${name}`
  }

  const footer = (
    <>
      {props.showImpressum && <a href={legal('impressum')}>Impressum</a>}
      {props.showPrivacy && <a href={legal('privacy')}>Privacy</a>}
      {props.showContact && <a href={legal('contact')}>Contact</a>}
      <span>gore on {props.site}</span>
    </>
  )

  return (
    <Shell
      site={props.site}
      isAdmin={props.isAdmin}
      logoutURL={props.logoutURL}
      shortPrefix={props.shortPrefix}
      footer={footer}
    >
      {props.isAdmin && (
        <Overview data={overview} days={overviewDays} site={props.site} />
      )}

      <LinkTable
        isAdmin={props.isAdmin}
        statsMode={props.statsMode}
        devMode={props.devMode}
        reloadKey={reloadKey}
        status={status}
        notify={notify}
        onNew={() => setCreating(true)}
        onChanged={refresh}
      />

      {creating && (
        <LinkForm
          record={null}
          onClose={() => setCreating(false)}
          onSaved={(alias) => {
            setCreating(false)
            notify(`${alias} created.`)
            refresh()
          }}
        />
      )}
    </Shell>
  )
}

export default Home
