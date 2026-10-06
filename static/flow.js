(() => {
  const svgNS = 'http://www.w3.org/2000/svg';
  const width = 232, height = 80;

  class FlowViewer {
    constructor(root, previous = {}) {
      this.root = root;
      // DOMParser parses noscript as live markup; the active viewer replaces it.
      root.querySelector('noscript')?.remove();
      this.nodes = new Map([...root.querySelectorAll('[data-flow-key]')].map(node => [node.dataset.flowKey, node]));
      this.details = new Map([...root.querySelectorAll('[data-flow-detail]')].map(detail => [detail.dataset.flowDetail, detail]));
      this.inspector = root.querySelector('[data-flow-inspector]');
      this.all = root.querySelector('[data-flow-all]');
      this.all.checked = previous.all || false;
      this.selected = null;
      this.root.addEventListener('click', event => this.click(event));
      this.root.addEventListener('change', event => { if (event.target === this.all) this.showEdges(); });
      this.root.addEventListener('keydown', event => {
        if (event.key === 'Escape' && this.selected) { event.preventDefault(); this.close(); }
      });
      this.root.addEventListener('focusin', event => {
        if (event.target.dataset.flowKey && event.target.matches(':focus-visible')) this.reveal(event.target.dataset.flowKey);
      });
      // Restore the inspector before measuring the graph, without navigating away
      // from the owner's captured viewport.
      if (previous.selected && this.nodes.has(previous.selected)) this.select(previous.selected, false, false);
      else if (previous.selected) this.feedback('removed');
      this.renderer = root.querySelector('[data-flow-renderer]');
      if (this.renderer) {
        try { this.graph(previous); }
        catch (_) {
          this.cy?.destroy(); this.cy = null;
          delete root.dataset.graphReady;
          root.querySelector('[data-flow-error]').hidden = false;
        }
      }
      this.timer = setInterval(() => this.checkRevision(), 10000);
    }

    graph(previous) {
      const connections = new Map();
      for (const [key, detail] of this.details) {
        detail.querySelectorAll('[data-flow-target]').forEach(link => {
          const id = JSON.stringify([key, link.dataset.flowTarget]);
          const primary = link.dataset.flowPrimary === 'true' || connections.get(id)?.data.primary === true;
          // The canvas shows one connection per destination; the inspector keeps
          // every trigger, including skip/edit paths sharing the same destination.
          connections.set(id, { data: { id, source: key, target: link.dataset.flowTarget, primary }, classes: primary ? 'primary' : 'secondary' });
        });
      }
      const edges = [...connections.values()];
      const primary = edges.filter(edge => edge.data.primary);
      this.cy = window.cytoscape({
        container: this.renderer,
        elements: [...this.nodes.keys()].map(id => ({ data: { id } })).concat(primary),
        layout: { name: 'preset' },
        autoungrabify: true, autounselectify: true,
        minZoom: .08, maxZoom: 1.6,
        style: this.style(),
      });
      // Secondary paths must never alter the readable forward layout.
      this.cy.layout({ name: 'dagre', rankDir: 'LR', rankSep: 90, nodeSep: 38, fit: false, animate: false }).run();
      this.cy.add(edges.filter(edge => !edge.data.primary));
      this.root.dataset.graphReady = '';
      this.cy.on('pan zoom render', () => this.renderNodes());
      this.cy.on('tap', event => {
        if (event.target === this.cy) this.close(false);
        else if (event.target.isNode()) this.select(event.target.id());
      });
      let initialFit = !previous.zoom;
      this.resize = new ResizeObserver(() => {
        this.cy.resize();
        if (initialFit) { this.fit(true); initialFit = false; }
        this.sizeInspector();
        this.renderNodes();
      });
      this.resize.observe(this.renderer);
      this.theme = new MutationObserver(() => this.cy.style(this.style()));
      this.theme.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] });
      if (previous.zoom) { this.cy.zoom(previous.zoom); this.cy.pan(previous.pan); }
      else this.fit();
      this.buildMinimap(primary);
      this.showEdges();
      this.renderNodes();
    }

    style() {
      const css = getComputedStyle(this.root);
      const color = name => css.getPropertyValue(name).trim();
      return [
        { selector: 'node', style: { width, height, 'background-opacity': 0, 'border-width': 0 } },
        { selector: 'edge', style: {
          width: 1.5, 'curve-style': 'round-taxi', 'taxi-direction': 'rightward', 'taxi-radius': 12, 'target-arrow-shape': 'triangle',
          'line-color': color('--piko-control-border'), 'target-arrow-color': color('--piko-control-border'),
          'arrow-scale': .7,
        } },
        { selector: 'edge.secondary', style: { display: 'none', 'curve-style': 'bezier', opacity: .6, 'control-point-step-size': 64 } },
        { selector: 'edge.visible', style: { display: 'element' } },
        { selector: 'edge.highlighted', style: { 'line-color': color('--color-primary'), 'target-arrow-color': color('--color-primary'), width: 2.5 } },
      ];
    }

    select(key, focus = false, navigate = true) {
      if (!this.nodes.has(key)) return;
      this.selected = key;
      this.nodes.forEach((node, id) => node.setAttribute('aria-pressed', String(id === key)));
      this.details.forEach((detail, id) => { detail.hidden = id !== key; });
      this.inspector.hidden = false;
      this.root.dataset.inspecting = '';
      this.root.querySelector('.piko-flow-picker').open = false;
      this.showEdges();
      this.resizeGraph();
      if (navigate) this.reveal(key);
      if (focus) this.inspector.focus({ preventScroll: true });
    }

    close(focus = true) {
      const node = this.nodes.get(this.selected);
      this.selected = null;
      this.nodes.forEach(node => node.setAttribute('aria-pressed', 'false'));
      this.inspector.hidden = true;
      delete this.root.dataset.inspecting;
      this.showEdges();
      this.resizeGraph();
      if (focus) node?.focus({ preventScroll: true });
    }

    showEdges() {
      if (!this.cy) return;
      this.cy.batch(() => this.cy.edges().forEach(edge => {
        const related = edge.source().id() === this.selected || edge.target().id() === this.selected;
        edge.toggleClass('visible', !edge.data('primary') && (this.all.checked || related));
        edge.toggleClass('highlighted', edge.data('primary') && related);
      }));
    }

    reveal(key) {
      if (!this.cy) return;
      const node = this.cy.getElementById(key), p = node.renderedPosition(), z = this.cy.zoom();
      const visibleHeight = this.selected && window.innerWidth < 768 ? this.inspector.getBoundingClientRect().top-this.renderer.getBoundingClientRect().top : this.cy.height();
      if (p.x - width*z/2 < 8 || p.x + width*z/2 > this.cy.width()-8 || p.y - height*z/2 < 8 || p.y + height*z/2 > visibleHeight-8) {
        const position = node.position();
        this.cy.pan({ x: this.cy.width()/2-position.x*z, y: visibleHeight/2-position.y*z });
      }
    }

    sizeInspector() {
      this.inspector.style.maxHeight = window.innerWidth < 768 ? Math.min(window.innerHeight*.45, this.renderer.clientHeight*.65)+'px' : '';
    }

    resizeGraph() { if (this.cy) { this.cy.resize(); this.sizeInspector(); this.renderNodes(); } }

    renderNodes() {
      if (!this.cy) return;
      const zoom = this.cy.zoom();
      for (const [key, button] of this.nodes) {
        const p = this.cy.getElementById(key).renderedPosition();
        button.style.transform = `translate(${p.x-width*zoom/2}px, ${p.y-height*zoom/2}px) scale(${zoom})`;
      }
      this.root.querySelector('[data-flow-zoom-label]').value = new Intl.NumberFormat('fa').format(Math.round(zoom*100))+'٪';
      if (this.miniViewport) {
        const e = this.cy.extent();
        setSVG(this.miniViewport, { x: e.x1, y: e.y1, width: e.w, height: e.h });
      }
    }

    fit(initial = false) {
      if (!this.cy) return;
      // Fit the nodes, independent of which secondary connections are visible.
      this.cy.fit(this.cy.nodes(), window.innerWidth < 768 ? 28 : 60);
      if (this.cy.zoom() > 1) this.cy.zoom({ level: 1, renderedPosition: { x: this.cy.width()/2, y: this.cy.height()/2 } });
      // Phones start with readable touch targets; Fit still offers the overview.
      if (initial && window.innerWidth < 768 && this.cy.zoom() < .65) {
        this.cy.zoom(.65);
        this.cy.center(this.cy.nodes().first());
      }
    }

    buildMinimap(primary) {
      const svg = this.root.querySelector('[data-flow-minimap]');
      const box = this.cy.nodes().boundingBox();
      svg.setAttribute('viewBox', `${box.x1-40} ${box.y1-40} ${box.w+80} ${box.h+80}`);
      for (const edge of primary) {
        const a = this.cy.getElementById(edge.data.source).position(), b = this.cy.getElementById(edge.data.target).position();
        const line = document.createElementNS(svgNS, 'line');
        setSVG(line, { x1: a.x, y1: a.y, x2: b.x, y2: b.y });
        svg.append(line);
      }
      this.cy.nodes().forEach(node => {
        const p = node.position(), rect = document.createElementNS(svgNS, 'rect');
        setSVG(rect, { x: p.x-width/2, y: p.y-height/2, width, height, rx: 12 });
        svg.append(rect);
      });
      this.miniViewport = document.createElementNS(svgNS, 'rect');
      this.miniViewport.setAttribute('class', 'piko-minimap-viewport');
      svg.append(this.miniViewport);
      svg.addEventListener('pointerdown', event => {
        event.preventDefault();
        const point = new DOMPoint(event.clientX, event.clientY).matrixTransform(svg.getScreenCTM().inverse());
        this.cy.pan({ x: this.cy.width()/2-point.x*this.cy.zoom(), y: this.cy.height()/2-point.y*this.cy.zoom() });
      });
    }

    click(event) {
      const node = event.target.closest('[data-flow-key], [data-flow-select], [data-flow-target]');
      if (node) {
        event.preventDefault();
        const key = node.dataset.flowKey || node.dataset.flowSelect || node.dataset.flowTarget;
        this.select(key, !node.dataset.flowKey || event.detail === 0);
        return;
      }
      if (event.target.closest('[data-flow-close]')) { this.close(); return; }
      if (event.target.closest('[data-flow-fit]')) { this.fit(); return; }
      const zoom = event.target.closest('[data-flow-zoom]');
      if (zoom && this.cy) this.cy.zoom({ level: this.cy.zoom()*(zoom.dataset.flowZoom === 'in' ? 1.2 : 1/1.2), renderedPosition: { x: this.cy.width()/2, y: this.cy.height()/2 } });
      if (event.target.closest('[data-flow-refresh]')) this.refresh();
    }

    feedback(message) {
      const feedback = this.root.querySelector('[data-flow-feedback]');
      feedback.textContent = feedback.dataset[message];
      feedback.classList.toggle('sr-only', message !== 'removed');
      feedback.classList.toggle('piko-flow-notice', message === 'removed');
    }

    async checkRevision() {
      if (document.hidden || this.checking || this.refreshing) return;
      this.checking = true;
      try {
        const url = new URL(this.root.dataset.flowUrl, location.origin);
        url.pathname += '/status';
        const response = await fetch(url, { cache: 'no-store', mode: 'same-origin', signal: AbortSignal.timeout(10000) });
        if (!response.ok) throw new Error('Flow unavailable');
        const state = await response.json();
        if (Number.isSafeInteger(state.revision)) this.root.querySelector('[data-flow-update]').hidden = String(state.revision) === this.root.dataset.flowRevision;
      } catch (_) { /* Keep the inspected snapshot readable; explicit refresh reports failures. */ }
      finally { this.checking = false; }
    }

    async refresh() {
      if (this.refreshing) return;
      this.refreshing = true;
      const button = this.root.querySelector('[data-flow-refresh]');
      button.disabled = true;
      try {
        const response = await fetch(this.root.dataset.flowUrl, { cache: 'no-store', mode: 'same-origin', signal: AbortSignal.timeout(10000) });
        if (!response.ok) throw new Error('Flow unavailable');
        const next = new DOMParser().parseFromString(await response.text(), 'text/html').querySelector('[data-flow-page]');
        if (!next || next.dataset.flowView !== this.root.dataset.flowView) throw new Error('Flow unavailable');
        const state = { selected: this.selected, all: this.all.checked, pan: this.cy?.pan(), zoom: this.cy?.zoom() };
        this.destroy();
        this.root.replaceWith(next);
        viewer = new FlowViewer(next, state);
        if (!state.selected || viewer.selected) viewer.feedback('refreshed');
        (viewer.selected ? viewer.inspector : next.querySelector('.piko-flow-versions a[aria-current]')).focus({ preventScroll: true });
      } catch (_) {
        this.root.querySelector('[data-flow-error]').hidden = false;
        button.disabled = false;
        this.refreshing = false;
      }
    }

    destroy() { clearInterval(this.timer); this.resize?.disconnect(); this.theme?.disconnect(); this.cy?.destroy(); }
  }

  function setSVG(element, values) { for (const [name, value] of Object.entries(values)) element.setAttribute(name, String(value)); }
  let viewer;
  const root = document.querySelector('[data-flow-page]');
  if (root) viewer = new FlowViewer(root);
  document.addEventListener('visibilitychange', () => { if (!document.hidden) viewer?.checkRevision(); });
  // A back/forward-cache entry resumes the same graph and revision observer.
  window.addEventListener('pagehide', event => { if (!event.persisted) viewer?.destroy(); });
})();
