// Shell is the frame every view sits in: which site you are looking at,
// the way out of the console, and where the legal pages are.
//
// The site name is shown rather than chosen. One process serves several
// hosts, but which one you administer is decided by the address you came
// in on, and a switcher would imply otherwise.
//
// The public index does not link to the console. That prefix is kept off
// this page so a proxy can hide it; a sign-in link would publish it.
const Shell = ({ site, isAdmin, logoutURL, shortPrefix, children, footer }) => (
  <main>
    <header className="top">
      <h1>
        <a href=".">gore</a>
      </h1>
      <p>Short links on {site}</p>
      <div className="who">
        {isAdmin && (
          <>
            <a className="link wide" href={shortPrefix || '.'}>
              Public index
            </a>
            <a className="link" href={logoutURL || window.location.pathname}>
              Sign out
            </a>
          </>
        )}
        <a className="wide" href="https://github.com/rukins/gore">
          GitHub
        </a>
      </div>
    </header>

    {children}

    <p className="foot">{footer}</p>
  </main>
)

export default Shell
