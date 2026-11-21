// A video frame with the extension's component (or the hosted widget, as an
// OBS source) on top. ?bg=bright|dark|busy&src=<url>[&w=&h=&pos=left]
(function () {
  var q = new URLSearchParams(location.search);
  var scene = document.getElementById('scene');
  scene.style.backgroundImage = 'url("/dev/static/frame-' + (q.get('bg') || 'busy').replace(/[^a-z]/g, '') + '.svg")';
  var src = q.get('src') || '';
  if (!/^\/(ext|before)\//.test(src)) return;
  var f = document.createElement('iframe');
  f.src = src;
  f.title = 'extension';
  var w = Number(q.get('w')) || 320;
  var h = Number(q.get('h')) || 160;
  f.width = w;
  f.height = h;
  f.allowTransparency = 'true';
  if (q.get('pos') === 'left') {
    f.style.left = '40px';
    f.style.bottom = '40px';
  } else {
    // Twitch's component area: the right side of the player, 25% of its height.
    f.style.right = '24px';
    f.style.top = '72px';
  }
  scene.appendChild(f);
})();
