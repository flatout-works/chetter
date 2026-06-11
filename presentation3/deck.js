const slides = Array.from(document.querySelectorAll('.slide'));
const currentEl = document.getElementById('current');
const totalEl = document.getElementById('total');
const progressEl = document.getElementById('progress');
const overviewEl = document.getElementById('overview');
const prevBtn = document.getElementById('prev');
const nextBtn = document.getElementById('next');
const gridToggle = document.getElementById('gridToggle');

let current = 0;

function clampSlide(index) {
  return Math.max(0, Math.min(slides.length - 1, index));
}

function setHash(index) {
  const target = `#${index + 1}`;
  if (window.location.hash !== target) {
    history.replaceState(null, '', target);
  }
}

function renderOverview() {
  overviewEl.innerHTML = '';
  slides.forEach((slide, index) => {
    const button = document.createElement('button');
    button.type = 'button';
    button.className = `thumb${index === current ? ' active' : ''}`;
    button.innerHTML = `<b>${index + 1}. ${slide.dataset.title || 'Slide'}</b><span>${slide.querySelector('h1,h2')?.textContent || ''}</span>`;
    button.addEventListener('click', () => {
      goTo(index);
      overviewEl.classList.remove('open');
    });
    overviewEl.appendChild(button);
  });
}

function goTo(index) {
  current = clampSlide(index);
  slides.forEach((slide, slideIndex) => {
    slide.classList.toggle('active', slideIndex === current);
  });
  currentEl.textContent = String(current + 1);
  totalEl.textContent = String(slides.length);
  progressEl.style.width = `${((current + 1) / slides.length) * 100}%`;
  prevBtn.disabled = current === 0;
  nextBtn.disabled = current === slides.length - 1;
  setHash(current);
  renderOverview();
}

function fromHash() {
  const hashValue = Number(window.location.hash.replace('#', ''));
  if (Number.isFinite(hashValue) && hashValue > 0) return hashValue - 1;
  return 0;
}

prevBtn.addEventListener('click', () => goTo(current - 1));
nextBtn.addEventListener('click', () => goTo(current + 1));
gridToggle.addEventListener('click', () => overviewEl.classList.toggle('open'));

document.addEventListener('keydown', (event) => {
  if (event.key === 'ArrowRight' || event.key === 'PageDown' || event.key === ' ') {
    event.preventDefault();
    goTo(current + 1);
  }
  if (event.key === 'ArrowLeft' || event.key === 'PageUp') {
    event.preventDefault();
    goTo(current - 1);
  }
  if (event.key === 'Home') {
    event.preventDefault();
    goTo(0);
  }
  if (event.key === 'End') {
    event.preventDefault();
    goTo(slides.length - 1);
  }
  if (event.key.toLowerCase() === 'g') overviewEl.classList.toggle('open');
  if (event.key === 'Escape') overviewEl.classList.remove('open');
  if (event.key.toLowerCase() === 'f' && document.fullscreenEnabled) {
    if (document.fullscreenElement) document.exitFullscreen();
    else document.documentElement.requestFullscreen();
  }
});

window.addEventListener('hashchange', () => goTo(fromHash()));

goTo(fromHash());
