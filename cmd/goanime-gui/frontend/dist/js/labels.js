// Built-in player options. `id` is what the backend receives; an empty id
// means mpv through the CLI's IPC launcher (full header support).
export const PLAYERS = [
  { id: "", label: "mpv", note: "recomendado" },
  { id: "vlc", label: "VLC" },
  { id: "mpc-hc", label: "MPC-HC" },
  { id: "wmplayer", label: "Windows Media Player" },
];

// GENRE_LABELS mirrors the Go map so catalog cards read in Portuguese
// without a round-trip per genre. An unknown genre passes through
// untranslated rather than vanishing.
const GENRE_LABELS = {
  Action: "Ação",
  Adventure: "Aventura",
  Comedy: "Comédia",
  Drama: "Drama",
  Ecchi: "Ecchi",
  Fantasy: "Fantasia",
  Horror: "Terror",
  "Mahou Shoujo": "Garota mágica",
  Mecha: "Mecha",
  Music: "Música",
  Mystery: "Mistério",
  Psychological: "Psicológico",
  Romance: "Romance",
  "Sci-Fi": "Ficção científica",
  "Slice of Life": "Slice of life",
  Sports: "Esportes",
  Supernatural: "Sobrenatural",
  Thriller: "Suspense",
};

export function genreLabel(name) {
  return GENRE_LABELS[name] || name;
}

export const FORMAT_LABELS = {
  TV: "Série de TV",
  TV_SHORT: "Curta de TV",
  MOVIE: "Filme",
  SPECIAL: "Especial",
  OVA: "OVA",
  ONA: "ONA",
  MUSIC: "Videoclipe",
};

export const STATUS_LABELS = {
  RELEASING: "Em exibição",
  FINISHED: "Finalizado",
  NOT_YET_RELEASED: "Não lançado",
  CANCELLED: "Cancelado",
  HIATUS: "Em hiato",
};
