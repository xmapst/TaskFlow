// Fit the rendered grid without changing the source terminal's columns or rows.
export function fitTerminalViewport(viewport: HTMLElement, host: HTMLElement): () => void {
  const screen = host.querySelector<HTMLElement>('.xterm-screen')
  if (!screen) return () => {}
  const fit = () => {
    const { offsetWidth: width, offsetHeight: height } = screen
    if (!width || !height) return
    viewport.style.setProperty('--terminal-aspect', `${width} / ${height}`)
    const { clientWidth, clientHeight } = viewport
    if (!clientWidth || !clientHeight) return
    host.style.width = `${width}px`
    host.style.height = `${height}px`
    host.style.setProperty('--terminal-zoom', String(Math.min(clientWidth / width, clientHeight / height)))
  }
  const observer = new ResizeObserver(fit)
  observer.observe(viewport)
  observer.observe(screen)
  fit()
  return () => observer.disconnect()
}
