declare global {
  interface Window {
    HEALTHBEAT_CONFIG?: { apiBaseUrl?: string }
  }
}

// Bir API yolu için mutlak URL. apiBaseUrl yapılandırılmamışsa yol origin'e göreli kalır;
// Vite dev proxy'sinin beklediği budur.
export function apiUrl(path: string): string {
  const base = (window.HEALTHBEAT_CONFIG?.apiBaseUrl ?? '').trim().replace(/\/+$/, '')
  return base + path
}
