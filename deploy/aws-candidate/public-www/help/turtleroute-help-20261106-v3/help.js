(function initTheme() {
      var root = document.documentElement;
      var themeSelect = document.querySelector("#theme-select");
      var queryTheme = new URLSearchParams(location.search).get("theme");
      var valid = { light: true, dark: true, auto: true };

      function applyTheme(theme) {
        if (!valid[theme]) theme = "auto";
        root.setAttribute("data-theme", theme);
        if (themeSelect) themeSelect.value = theme;
      }

      if (valid[queryTheme]) {
        applyTheme(queryTheme);
      } else {
        try {
          var stored = localStorage.getItem("theme") || localStorage.getItem("color-theme") || localStorage.getItem("appearance");
          if (valid[stored]) applyTheme(stored);
        } catch (e) {}

        try {
          var parentTheme = window.parent && window.parent.document && (
            window.parent.document.documentElement.getAttribute("data-theme") ||
            window.parent.document.body.getAttribute("data-theme") ||
            window.parent.document.documentElement.classList.contains("dark") && "dark" ||
            window.parent.document.documentElement.classList.contains("light") && "light"
          );
          if (valid[parentTheme]) applyTheme(parentTheme);
        } catch (e) {}
      }

      if (themeSelect) {
        themeSelect.value = root.getAttribute("data-theme") || "auto";
        themeSelect.addEventListener("change", function(event) {
          applyTheme(event.target.value);
          try { localStorage.setItem("theme", event.target.value); } catch (e) {}
        });
      }
      window.addEventListener("message", function(event) {
        if (event.source !== window.parent || ![location.origin, "https://www.turtleligpt.com", "https://api.turtleligpt.com"].includes(event.origin)) return;
        var data = event.data || {};
        var theme = typeof data === "string" ? data : (data.theme || data.appearance);
        if (valid[theme]) applyTheme(theme);
      });
    })();

    document.querySelectorAll(".copy").forEach((button) => {
      button.addEventListener("click", async () => {
        const target = document.querySelector(button.dataset.copy);
        if (!target) return;
        const text = target.innerText;
        try {
          await navigator.clipboard.writeText(text);
          const old = button.innerText;
          button.innerText = "已复制";
          setTimeout(() => button.innerText = old, 1400);
        } catch (e) {
          button.innerText = "请手动复制";
        }
      });
    });

    const search = document.querySelector('#help-search');
    const sections = [...document.querySelectorAll('.content section')];
    const toc = document.querySelector('.toc');
    const tocLinks = [...document.querySelectorAll('#help-toc a')];
    const pathLinks = [...document.querySelectorAll('.start-path .path-step')];
    const sectionLinks = tocLinks.concat(pathLinks);
    const faqDefaults = new Map([...document.querySelectorAll('details')].map(el => [el, el.open]));
    function filterHelp() {
      const query = search.value.trim().toLowerCase();
      let count = 0;
      for (const section of sections) {
        const match = !query || section.textContent.toLowerCase().includes(query);
        section.hidden = !match; if (match) count++;
        const link = tocLinks.find(link => link.hash === '#' + section.id);
        if (link) link.hidden = !match;
        for (const detail of section.querySelectorAll('details')) {
          detail.hidden = Boolean(query) && !detail.textContent.toLowerCase().includes(query);
          detail.open = query ? !detail.hidden : faqDefaults.get(detail);
        }
      }
      const status = document.querySelector('#search-feedback');
      status.hidden = !query; status.textContent = '找到 ' + count + ' 个相关章节';
      document.querySelector('#search-empty').hidden = count > 0;
    }
    search.addEventListener('input', filterHelp);
    document.querySelector('#clear-search').addEventListener('click', () => { search.value = ''; filterHelp(); search.focus(); });
    const tocToggle = document.querySelector('.toc-toggle');
    tocToggle.addEventListener('click', () => {
      const expanded = toc.dataset.expanded !== 'true';
      toc.dataset.expanded = String(expanded); tocToggle.setAttribute('aria-expanded', String(expanded));
      tocToggle.querySelector('span').textContent = expanded ? '−' : '＋';
    });
    document.querySelectorAll('a[href^="#"]').forEach(link => link.addEventListener('click', () => {
      if (search.value) { search.value = ''; filterHelp(); }
      toc.dataset.expanded = 'false'; tocToggle.setAttribute('aria-expanded', 'false'); tocToggle.querySelector('span').textContent = '＋';
    }));
    const observer = new IntersectionObserver(entries => {
      const entry = entries.find(entry => entry.isIntersecting && !entry.target.hidden);
      if (!entry) return;
      sectionLinks.forEach(link => { if (link.hash === '#' + entry.target.id) link.setAttribute('aria-current','location'); else link.removeAttribute('aria-current'); });
    }, {rootMargin:'-10% 0px -65% 0px'});
    sections.forEach(section => observer.observe(section));
    window.addEventListener('pagehide', event => { if (!event.persisted) observer.disconnect(); });
    const aliases = {'#migration':'#codex','#subscribe':'#choose-model','#safe':'#support'};
if (aliases[location.hash]) { const target=aliases[location.hash]; history.replaceState(null,'',target); document.querySelector(target).scrollIntoView(); }

  
const imageDialog=document.querySelector('.image-dialog');
let imageTrigger;
document.querySelectorAll('.image-button').forEach(button=>button.addEventListener('click',()=>{
 imageTrigger=button;
 const source=button.querySelector('img');
 const clone=document.createElement('div');
 clone.className=button.className;
 clone.classList.toggle('portrait',source.width<source.height*1.4);
 button.childNodes.forEach(node=>clone.append(node.cloneNode(true)));
 clone.querySelector('img').removeAttribute('loading');
 document.querySelector('#image-title').textContent=button.dataset.imageTitle;
 imageDialog.querySelector('.dialog-media').replaceChildren(clone);
 imageDialog.querySelector('.dialog-caption').textContent=button.dataset.imageCaption;
 imageDialog.showModal();
}));
function closeImage(){imageDialog.close();}
imageDialog.querySelector('.dialog-close').addEventListener('click',closeImage);
imageDialog.addEventListener('click',event=>{if(event.target===imageDialog){const rect=imageDialog.getBoundingClientRect();if(event.clientX<rect.left||event.clientX>rect.right||event.clientY<rect.top||event.clientY>rect.bottom)closeImage();}});
imageDialog.addEventListener('close',()=>{imageDialog.querySelector('.dialog-media').replaceChildren();imageTrigger?.focus({preventScroll:true});});
