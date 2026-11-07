import Trend from './Trend'

const fmt = (n) => (n ?? 0).toLocaleString()

// pct renders a share without pretending to precision it does not have.
const pct = (part, whole) =>
  whole > 0 ? `${((part / whole) * 100).toFixed(1)}%` : '—'

const Tile = ({ label, value, note }) => (
  <div className="card tile">
    <div className="label">{label}</div>
    <div className="value">{value}</div>
    <div className="note">{note}</div>
  </div>
)

// Overview is the console's first answer: whether anything is happening.
//
// Four figures and the shape of the month. The figures are the ones an
// operator acts on, and automated traffic is among them because it is a
// large share here and leaving it out would make the others look wrong.
const Overview = ({ data, days, site }) => {
  const o = data ?? {}
  const series = o.series ?? []
  const ready = data !== null && data !== undefined

  return (
    <>
      <section className="tiles" aria-label="Totals">
        <Tile
          label="Links"
          value={ready ? fmt(o.links) : '–'}
          note={`on ${site}`}
        />
        <Tile
          label="Visits"
          value={ready ? fmt(o.visits) : '–'}
          note={`in the last ${days} days, bots included`}
        />
        <Tile
          label="By people"
          value={ready ? fmt(o.people) : '–'}
          note="counted in the charts"
        />
        <Tile
          label="Automated"
          value={ready ? pct(o.bots, o.visits) : '–'}
          note={ready ? `${fmt(o.bots)} visits, not in the charts` : ' '}
        />
      </section>

      <figure className="card chart" style={{ margin: '0 0 10px' }}>
        <Trend
          title="Daily visits by people"
          labels={series.map((d) => d.day)}
          pv={series.map((d) => d.pv)}
          uv={series.map((d) => d.uv)}
          loading={!ready}
        />
      </figure>
    </>
  )
}

export default Overview
