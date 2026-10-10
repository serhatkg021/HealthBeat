// Tema, uygulamadan önce ve ilk boyamadan önce uygulanır (koyu temada beyaz parlama olmasın; bkz. index.html). CSP satır
// içi script'e izin vermediği için ayrı bir dosyadır. Seçim tarayıcıda saklanır (src/theme.ts ile aynı anahtar): "light",
// "dark" ya da yok/"system" = işletim sisteminin ayarı. Kök elemana data-theme="light"|"dark" yazar.
;(function () {
  var choice = 'system'
  try {
    var saved = localStorage.getItem('healthbeat_theme')
    if (saved === 'light' || saved === 'dark') choice = saved
  } catch {
    // Gizli pencere ya da engellenmiş depolama: sistem ayarı.
  }
  var dark = choice === 'dark' || (choice === 'system' && window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches)
  document.documentElement.setAttribute('data-theme', dark ? 'dark' : 'light')
})()
