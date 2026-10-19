// The face of a creeper, drawn for this site as 8×8 pixels: "#" is dark.
// The home page's lock turns into one (HeroLock.vue), the toast that
// follows shows one, and so does the page of a missing page.
export const face = [
  '........',
  '........',
  '.##..##.',
  '.##..##.',
  '...##...',
  '..####..',
  '..####..',
  '..#..#..',
]

export const dark = '#1a2019'

/** facePixels lists the dark pixels of the face, by x and y within 8×8. */
export const facePixels = face.flatMap((row, y) => [...row].flatMap((c, x) => (c === '#' ? [{ x, y }] : [])))

// The greens of the lock, mottled as a creeper's skin is.
const skin = ['#52b548', '#6dbd66', '#52b548', '#82cb7b', '#52b548', '#2b7a33']

/** headPixels colors every pixel of the head: the face, on its skin. */
export const headPixels = face.flatMap((row, y) =>
  [...row].map((c, x) => ({ x, y, fill: c === '#' ? dark : skin[(x * 5 + y * 3 + ((x * y) % 3)) % skin.length] })),
)
