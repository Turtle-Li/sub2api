import './shared.js';

const platformBar = document.querySelector('.model-presence');
if (platformBar instanceof HTMLElement) {
  let visible = true;
  const preference = matchMedia('(prefers-reduced-motion: reduce)');
  const update = () => {
    platformBar.dataset.paused = String(document.body.dataset.motionPaused === 'true' || !visible || document.hidden || preference.matches);
  };
  const observer = new IntersectionObserver(entries => { visible = entries.some(entry => entry.isIntersecting); update(); });
  observer.observe(platformBar);
  document.addEventListener('homepage:motion', update);
  document.addEventListener('visibilitychange', update);
  preference.addEventListener('change', update);
  update();
  window.addEventListener('pagehide', event => {
    if (event.persisted) return;
    observer.disconnect();
    document.removeEventListener('homepage:motion', update);
    document.removeEventListener('visibilitychange', update);
    preference.removeEventListener('change', update);
  });
}

const hero = document.querySelector('.ai-hero');
if (hero instanceof HTMLElement) {
  const sky = hero.querySelector('[data-hero-sky]');
  if (sky instanceof HTMLElement) {
    let visible = false;
    const updateSky = () => { sky.dataset.active = String(visible && !document.hidden); };
    const observer = new IntersectionObserver(entries => {
      visible = entries.some(entry => entry.isIntersecting);
      updateSky();
    });
    observer.observe(hero);
    document.addEventListener('visibilitychange', updateSky);
    window.addEventListener('pagehide', event => {
      if (event.persisted) return;
      observer.disconnect();
      document.removeEventListener('visibilitychange', updateSky);
    });
  }
  hero.querySelector('[data-browse-models]')?.addEventListener('click', event => {
    if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
    const search = document.querySelector('[data-search]');
    if (search instanceof HTMLInputElement && search.value) {
      search.value = '';
      search.dispatchEvent(new Event('input'));
    }
    document.querySelector('.provider-filter[data-provider="all"][aria-pressed="false"]')?.click();
  });
}
