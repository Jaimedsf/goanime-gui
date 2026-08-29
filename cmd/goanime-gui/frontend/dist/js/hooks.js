// The two places where modules would otherwise call each other both ways.
//
// A card knows how to open a title, and the episode view knows how to paint a
// card's star. A star toggles the library, and the library is made of cards.
// Importing in both directions closes a cycle, so cards.js calls through here
// and main.js connects the wires at boot — the same reason views.js takes its
// tab loaders from registerTab instead of importing the tab modules.
//
// Deliberately two named slots rather than a general event bus: these are the
// only two edges that need it, and a slot that is never filled fails loudly at
// the call site instead of silently going nowhere.
/**
 * @type {{
 *   openTitle: (r: SearchResult) => void,
 *   libraryChanged: () => void,
 * }}
 */
export const hooks = {
  // Open a title's episode list. cards.js -> episodes.js
  openTitle: () => {},
  // The favorites list changed. cards.js -> library.js and schedule.js
  libraryChanged: () => {},
};
