// Player-head URLs. In the desktop shell the Go asset server composes and
// caches heads at /pf/avatar/ (see app/avatars.go); in plain-browser dev that
// handler doesn't exist, so fall back to mc-heads.net directly and an inline
// SVG for offline/cracked players (who have no Mojang skin anywhere).

export const OFFLINE_SVG = 'data:image/svg+xml;utf8,' + encodeURIComponent(
  '<svg xmlns="http://www.w3.org/2000/svg" width="8" height="8" viewBox="0 0 8 8" shape-rendering="crispEdges">' +
  '<rect width="8" height="8" fill="#b58d6d"/><rect width="8" height="2" fill="#3b2d22"/>' +
  '<rect x="1" y="4" width="1" height="1" fill="#fff"/><rect x="2" y="4" width="1" height="1" fill="#523d91"/>' +
  '<rect x="5" y="4" width="1" height="1" fill="#523d91"/><rect x="6" y="4" width="1" height="1" fill="#fff"/>' +
  '<rect x="3" y="5" width="2" height="1" fill="#7a5b47"/><rect x="2" y="6" width="4" height="1" fill="#8a5a44"/></svg>')

/** URL for a player's head at the given pixel size (16–128). */
export function avatarUrl(id: string, size: number): string {
  if ((window as any).__pfDevMock) {
    if (id.startsWith('offline:')) return OFFLINE_SVG
    return `https://mc-heads.net/avatar/${id}/${size}`
  }
  return `/pf/avatar/${id}.png?size=${size}`
}
