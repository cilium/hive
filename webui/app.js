import * as THREE from "https://unpkg.com/three@0.160.0/build/three.module.js";

const state = {
  graph: null,
  nodes: new Map(),
  nodeMeshes: [],
  edges: [],
  edgeMeshes: [],
  moduleDeps: new Map(),
  moduleDepObjects: new Map(),
  expandedModules: new Set(),
  selectedId: null,
  moduleOrder: [],
  objectIndex: [],
  objectRefIndex: new Map(),
  searchEntries: [],
  history: [],
  historyLocked: false,
};

const ROOT_KEY = "__root__";

const palette = {
  module: "#1f7a8c",
  highlight: "#f5b700",
  connected: "#00a6d6",
  edge: "rgba(47, 72, 88, 0.35)",
};

const EDGE_TOOLTIP_MAX = 8;

const viewport = document.getElementById("viewport");
const tooltip = document.getElementById("tooltip");
const detailsBody = document.getElementById("details-body");
const detailsPanel = document.getElementById("details");
const moduleList = document.getElementById("module-list");
const objectQuery = document.getElementById("object-query");
const objectResults = document.getElementById("object-results");
const navBackButton = document.getElementById("nav-back");

const DEFAULT_ZOOM = 1.2;

const scene = new THREE.Scene();
const renderer = new THREE.WebGLRenderer({ antialias: true, alpha: true });
renderer.setPixelRatio(window.devicePixelRatio || 1);
viewport.appendChild(renderer.domElement);

const camera = new THREE.OrthographicCamera(-1, 1, 1, -1, -1000, 1000);
camera.position.set(0, 0, 200);
camera.zoom = DEFAULT_ZOOM;

const nodeGroup = new THREE.Group();
const treeEdgeGroup = new THREE.Group();
const depEdgeGroup = new THREE.Group();
scene.add(treeEdgeGroup, depEdgeGroup, nodeGroup);

const raycaster = new THREE.Raycaster();
const pointer = new THREE.Vector2();

let isDragging = false;
let lastPointer = null;
let zoomTween = null;
let panTween = null;
let pulseTargets = new Set();

function resize() {
  const rect = viewport.getBoundingClientRect();
  renderer.setSize(rect.width, rect.height);
  const aspect = rect.width / rect.height;
  const viewSize = 140;
  camera.left = -viewSize * aspect;
  camera.right = viewSize * aspect;
  camera.top = viewSize;
  camera.bottom = -viewSize;
  camera.updateProjectionMatrix();
}

window.addEventListener("resize", resize);
resize();

function animate() {
  requestAnimationFrame(animate);
  const now = performance.now();
  if (zoomTween) {
    const t = Math.min(1, (now - zoomTween.start) / zoomTween.duration);
    const eased = t < 0.5 ? 2 * t * t : 1 - Math.pow(-2 * t + 2, 2) / 2;
    camera.zoom = zoomTween.from + (zoomTween.to - zoomTween.from) * eased;
    camera.updateProjectionMatrix();
    if (t >= 1) {
      zoomTween = null;
    }
  }
  if (panTween) {
    const t = Math.min(1, (now - panTween.start) / panTween.duration);
    const eased = t < 0.5 ? 2 * t * t : 1 - Math.pow(-2 * t + 2, 2) / 2;
    camera.position.x = panTween.from.x + (panTween.to.x - panTween.from.x) * eased;
    camera.position.y = panTween.from.y + (panTween.to.y - panTween.from.y) * eased;
    if (t >= 1) {
      panTween = null;
    }
  }
  if (pulseTargets.size > 0) {
    const pulseTime = now / 140;
    pulseTargets.forEach((node) => {
      const factor = 1 + 0.06 * Math.sin(pulseTime);
      const scaleX = node.baseScale.x * factor;
      const scaleY = node.baseScale.y * factor;
      node.mesh.scale.set(scaleX, scaleY, 1);
    });
  }
  renderer.render(scene, camera);
}

function fetchGraph() {
  return fetch("/graph.json").then((res) => res.json());
}

function buildModuleDeps() {
  state.moduleDeps.clear();
  state.moduleDepObjects.clear();
  Object.keys(state.graph.modules || {}).forEach((path) => {
    state.moduleDeps.set(path, new Set());
  });

  (state.graph.edges || []).forEach((edge) => {
    if (edge.kind !== "depends" && edge.kind !== "invokes") {
      return;
    }
    const fromModule = moduleForNodeId(edge.from);
    const toModule = moduleForNodeId(edge.to);
    if (!fromModule || !toModule || fromModule === toModule) {
      return;
    }
    if (!state.graph.modules[fromModule] || !state.graph.modules[toModule]) {
      return;
    }
    state.moduleDeps.get(toModule).add(fromModule);

    if (!state.moduleDepObjects.has(toModule)) {
      state.moduleDepObjects.set(toModule, new Map());
    }
    const providerMap = state.moduleDepObjects.get(toModule);
    if (!providerMap.has(fromModule)) {
      providerMap.set(fromModule, new Set());
    }
    const labels = providerMap.get(fromModule);
    const obj = state.graph.objects && state.graph.objects[edge.from];
    if (obj) {
      labels.add(formatObject(obj));
    }
  });
}

function moduleForNodeId(id) {
  if (!id) {
    return "";
  }
  if (id.startsWith("module:")) {
    return id.slice("module:".length);
  }
  if (state.graph.constructors && state.graph.constructors[id]) {
    return state.graph.constructors[id].modulePath || "";
  }
  if (state.graph.invokers && state.graph.invokers[id]) {
    return state.graph.invokers[id].modulePath || "";
  }
  if (state.graph.decorators && state.graph.decorators[id]) {
    return state.graph.decorators[id].modulePath || "";
  }
  if (state.graph.objects && state.graph.objects[id]) {
    return state.graph.objects[id].modulePath || "";
  }
  return "";
}

function buildObjectIndex() {
  state.objectIndex = [];
  state.objectRefIndex = new Map();
  state.searchEntries = [];

  Object.values(state.graph.objects || {}).forEach((obj) => {
    const label = formatObject(obj);
    const providers = providerModulesForObject(obj);
    if (providers.length === 0) {
      return;
    }
    const entry = {
      id: obj.id,
      type: obj.type,
      name: obj.name || "",
      group: obj.group || "",
      label,
      providers,
      isPrivate: isPrivateObject(obj),
    };
    state.objectIndex.push(entry);
    const signature = objectSignature(obj.type, obj.name, obj.group);
    if (!state.objectRefIndex.has(signature)) {
      state.objectRefIndex.set(signature, []);
    }
    state.objectRefIndex.get(signature).push(entry);
    providers.forEach((modulePath) => {
      state.searchEntries.push({
        label,
        modulePath,
        signature,
        searchKey: label.toLowerCase(),
        isPrivate: entry.isPrivate,
      });
    });
  });

  state.searchEntries.sort((a, b) => {
    const label = a.label.localeCompare(b.label);
    if (label !== 0) {
      return label;
    }
    return a.modulePath.localeCompare(b.modulePath);
  });
}

function buildNodes() {
  state.nodes.clear();
  state.nodeMeshes = [];
  nodeGroup.clear();

  const modules = Object.keys(state.graph.modules || {});
  modules.sort();
  state.moduleOrder = modules;

  const rootSprite = createRootSprite();
  rootSprite.userData = {
    id: `module:${ROOT_KEY}`,
    label: "root",
    modulePath: ROOT_KEY,
    type: "module",
  };
  const rootNode = {
    id: `module:${ROOT_KEY}`,
    modulePath: ROOT_KEY,
    mesh: rootSprite,
    baseScale: { x: rootSprite.scale.x, y: rootSprite.scale.y },
  };
  rootSprite.renderOrder = 2;
  state.nodes.set(rootNode.id, rootNode);
  state.nodeMeshes.push(rootSprite);
  nodeGroup.add(rootSprite);

  modules.forEach((path) => {
    const nodeId = `module:${path}`;
    const sprite = createModuleSprite(moduleDisplayName(path));
    sprite.userData = { id: nodeId, label: path, modulePath: path, type: "module" };
    const node = {
      id: nodeId,
      modulePath: path,
      mesh: sprite,
      baseScale: { x: sprite.scale.x, y: sprite.scale.y },
    };
    sprite.renderOrder = 2;
    state.nodes.set(nodeId, node);
    state.nodeMeshes.push(sprite);
    nodeGroup.add(sprite);
  });
}

function layoutTree() {
  const positions = new Map();
  const edges = [];

  const roots = state.moduleOrder.filter((path) => {
    const parent = state.graph.modules[path]?.parent || "";
    return parent === "" || !state.graph.modules[parent];
  });
  roots.sort();

  const countVisible = (modulePath) => {
    let count = 1;
    if (!state.expandedModules.has(modulePath)) {
      return count;
    }
    const children = (state.graph.modules[modulePath]?.children || []).slice();
    children.sort();
    children.forEach((child) => {
      count += countVisible(child);
    });
    return count;
  };

  const place = (modulePath, depth, centerY) => {
    positions.set(modulePath, { x: depth, y: centerY });
    if (!state.expandedModules.has(modulePath)) {
      return;
    }
    const children = (state.graph.modules[modulePath]?.children || []).slice();
    children.sort();
    const sizes = children.map((child) => countVisible(child));
    const total = sizes.reduce((sum, val) => sum + val, 0);
    let cursor = centerY - (total / 2);
    children.forEach((child, idx) => {
      const childSize = sizes[idx];
      const childCenter = cursor + childSize / 2;
      edges.push([modulePath, child]);
      place(child, depth + 1, childCenter);
      cursor += childSize;
    });
  };

  positions.set(ROOT_KEY, { x: 0, y: 0 });

  const rootSizes = roots.map((root) => countVisible(root));
  const totalRoots = rootSizes.reduce((sum, val) => sum + val, 0);
  let cursor = -totalRoots / 2;
  roots.forEach((root, idx) => {
    const size = rootSizes[idx];
    const centerY = cursor + size / 2;
    edges.push([ROOT_KEY, root]);
    place(root, 1, centerY);
    cursor += size;
  });

  return { positions, edges };
}

function applyLayout(layout) {
  const xSpacing = 40;
  const ySpacing = 14;
  const visible = new Set();
  const root = layout.positions.get(ROOT_KEY);
  const rootOffsetX = root ? root.x * xSpacing : 0;
  const rootOffsetY = root ? root.y * ySpacing : 0;

  layout.positions.forEach((pos, modulePath) => {
    const node = state.nodes.get(`module:${modulePath}`);
    if (!node) {
      return;
    }
    node.mesh.visible = true;
    node.mesh.position.set(
      pos.x * xSpacing - rootOffsetX,
      -pos.y * ySpacing + rootOffsetY,
      1
    );
    visible.add(node.id);
  });

  state.nodes.forEach((node) => {
    if (!visible.has(node.id)) {
      node.mesh.visible = false;
    }
  });

  state.edges = layout.edges;
  rebuildEdges();
}

function rebuildEdges() {
  treeEdgeGroup.clear();
  depEdgeGroup.clear();
  state.edgeMeshes = [];

  state.edges.forEach(([from, to]) => {
    const fromNode = state.nodes.get(`module:${from}`);
    const toNode = state.nodes.get(`module:${to}`);
    if (!fromNode || !toNode || !fromNode.mesh.visible || !toNode.mesh.visible) {
      return;
    }
    const curve = new THREE.LineCurve3(
      flatPos(fromNode.mesh.position),
      flatPos(toNode.mesh.position)
    );
    const geometry = new THREE.TubeGeometry(curve, 20, 0.45, 8, false);
    const material = new THREE.MeshBasicMaterial({
      color: "rgba(47, 72, 88, 0.45)",
      transparent: true,
      opacity: 0.7,
    });
    const tube = new THREE.Mesh(geometry, material);
    tube.renderOrder = 0;
    tube.position.z = -1;
    tube.userData = { from: fromNode.id, to: toNode.id };
    treeEdgeGroup.add(tube);
    state.edgeMeshes.push(tube);
  });

  const { deps: visibleDeps, objects: edgeObjects } = buildVisibleModuleDeps();
  visibleDeps.forEach((deps, modulePath) => {
    if (modulePath === ROOT_KEY || !state.graph.modules[modulePath]) {
      return;
    }
    deps.forEach((dep) => {
      if (dep === ROOT_KEY || !state.graph.modules[dep]) {
        return;
      }
      const fromNode = state.nodes.get(`module:${modulePath}`);
      const toNode = state.nodes.get(`module:${dep}`);
      if (!fromNode || !toNode || !fromNode.mesh.visible || !toNode.mesh.visible) {
        return;
      }
      const curve = buildCurvedCurve(
        fromNode.mesh.position,
        toNode.mesh.position,
        curveBias(`${modulePath}->${dep}`)
      );
      const geometry = new THREE.TubeGeometry(curve, 28, 0.5, 8, false);
      const material = new THREE.MeshBasicMaterial({
        color: "rgba(242, 106, 79, 0.75)",
        transparent: true,
        opacity: 0.75,
      });
      const tube = new THREE.Mesh(geometry, material);
      tube.renderOrder = 0;
      tube.position.z = -1;
      const key = `${modulePath}->${dep}`;
      tube.userData = {
        from: fromNode.id,
        to: toNode.id,
        kind: "dep",
        fromModule: modulePath,
        toModule: dep,
        objects: edgeObjects.get(key) || [],
      };
      depEdgeGroup.add(tube);
      state.edgeMeshes.push(tube);
    });
  });

  applyHighlight();
}

function buildVisibleModuleDeps() {
  const visibleDeps = new Map();
  const objectsByEdge = new Map();
  const visibleCache = new Map();

  const resolveVisible = (modulePath) => {
    if (!modulePath || modulePath === ROOT_KEY) {
      return ROOT_KEY;
    }
    if (visibleCache.has(modulePath)) {
      return visibleCache.get(modulePath);
    }
    let current = modulePath;
    while (current) {
      const node = state.nodes.get(`module:${current}`);
      if (node && node.mesh.visible) {
        visibleCache.set(modulePath, current);
        return current;
      }
      const parent = state.graph.modules[current]?.parent || "";
      if (!parent || parent === current) {
        break;
      }
      current = parent;
    }
    visibleCache.set(modulePath, ROOT_KEY);
    return ROOT_KEY;
  };

  state.moduleDepObjects.forEach((providers, consumer) => {
    const fromVisible = resolveVisible(consumer);
    if (!fromVisible || fromVisible === ROOT_KEY) {
      return;
    }
    providers.forEach((objects, provider) => {
      const toVisible = resolveVisible(provider);
      if (!toVisible || toVisible === ROOT_KEY || toVisible === fromVisible) {
        return;
      }
      if (!visibleDeps.has(fromVisible)) {
        visibleDeps.set(fromVisible, new Set());
      }
      visibleDeps.get(fromVisible).add(toVisible);
      const edgeKey = `${fromVisible}->${toVisible}`;
      if (!objectsByEdge.has(edgeKey)) {
        objectsByEdge.set(edgeKey, new Set());
      }
      objects.forEach((label) => objectsByEdge.get(edgeKey).add(label));
    });
  });

  if (visibleDeps.size === 0) {
    state.moduleDeps.forEach((deps, modulePath) => {
      const fromVisible = resolveVisible(modulePath);
      if (!fromVisible || fromVisible === ROOT_KEY) {
        return;
      }
      deps.forEach((dep) => {
        const toVisible = resolveVisible(dep);
        if (!toVisible || toVisible === ROOT_KEY || toVisible === fromVisible) {
          return;
        }
        if (!visibleDeps.has(fromVisible)) {
          visibleDeps.set(fromVisible, new Set());
        }
        visibleDeps.get(fromVisible).add(toVisible);
      });
    });
  }

  const objects = new Map();
  objectsByEdge.forEach((set, key) => {
    const list = Array.from(set);
    list.sort();
    objects.set(key, list);
  });

  return { deps: visibleDeps, objects };
}

function buildCurvedCurve(fromPos, toPos, bias) {
  const start = flatPos(fromPos);
  const end = flatPos(toPos);
  const dx = end.x - start.x;
  const dy = end.y - start.y;
  const dist = Math.hypot(dx, dy);
  if (!dist) {
    return new THREE.LineCurve3(start, end);
  }
  const nx = -dy / dist;
  const ny = dx / dist;
  const bend = Math.min(24, dist * 0.25) * bias;
  const mid = new THREE.Vector3(
    (start.x + end.x) / 2 + nx * bend,
    (start.y + end.y) / 2 + ny * bend,
    0
  );
  return new THREE.QuadraticBezierCurve3(start, mid, end);
}

function flatPos(pos) {
  return new THREE.Vector3(pos.x, pos.y, 0);
}

function curveBias(key) {
  let hash = 0;
  for (let i = 0; i < key.length; i += 1) {
    hash = (hash * 31 + key.charCodeAt(i)) % 97;
  }
  return hash % 2 === 0 ? 1 : -1;
}

function updateGraph() {
  const layout = layoutTree();
  applyLayout(layout);
  buildModuleList();
}

function buildModuleList() {
  const list = moduleList || document.getElementById("module-list");
  const previousScroll = list ? list.scrollTop : 0;
  list.innerHTML = "";

  const buildTreeRow = (path, depth) => {
    const module = state.graph.modules[path];
    const row = document.createElement("div");
    row.className = "module-row";
    row.dataset.module = path;
    row.style.marginLeft = `${12 + depth * 12}px`;

    const label = document.createElement("div");
    label.className = "module-label";

    const title = document.createElement("div");
    title.className = "module-title";
    title.textContent = moduleDisplayName(path);

    const desc = document.createElement("div");
    desc.className = "module-desc";
    desc.textContent = module.description || "";

    label.appendChild(title);
    if (module.description) {
      label.appendChild(desc);
    }

    const badge = document.createElement("div");
    badge.className = "badge";
    const hasChildren = (state.graph.modules[path].children || []).length > 0;
    const isOpen = state.expandedModules.has(path);
    badge.textContent = hasChildren ? (isOpen ? "▾" : "▸") : "•";

    row.appendChild(label);
    row.appendChild(badge);

    row.addEventListener("click", (event) => {
      event.stopPropagation();
      pushHistory();
      if (hasChildren) {
        toggleModule(path);
      } else {
        state.expandedModules.add(path);
        updateGraph();
      }
      focusModule(path);
    });

    list.appendChild(row);

    const isExpanded = state.expandedModules.has(path);
    if (hasChildren && isExpanded) {
      const children = state.graph.modules[path].children || [];
      children.forEach((child) => buildTreeRow(child, depth + 1));
    }
  };

  const roots = state.graph.rootModules || [];
  roots.forEach((path) => buildTreeRow(path, 0));

  if (list) {
    list.scrollTop = previousScroll;
  }
}

function setupObjectSearch() {
  if (!objectQuery || !objectResults) {
    return;
  }
  objectQuery.addEventListener("input", () => {
    renderSearchResults(objectQuery.value);
  });
  objectQuery.addEventListener("keydown", (event) => {
    if (event.key === "Enter") {
      const first = objectResults.querySelector(".object-result");
      if (first) {
        first.click();
      }
    }
  });
  renderSearchResults("");
}

function renderSearchResults(query) {
  if (!objectResults) {
    return;
  }
  objectResults.innerHTML = "";
  const q = (query || "").trim().toLowerCase();
  if (!q) {
    objectResults.innerHTML = '<div class="muted">Type to search for objects.</div>';
    return;
  }
  const matches = state.searchEntries.filter((entry) => entry.searchKey.includes(q));
  if (matches.length === 0) {
    objectResults.innerHTML = '<div class="muted">No matches.</div>';
    return;
  }
  matches.slice(0, 50).forEach((entry) => {
    const row = document.createElement("button");
    row.type = "button";
    row.className = `object-result${entry.isPrivate ? " private" : ""}`;
    row.innerHTML = `
      <div class="object-result-title">${escapeHtml(entry.label)}</div>
      <div class="object-result-module">${escapeHtml(entry.modulePath)}</div>
    `;
    row.addEventListener("click", () => {
      focusModulePath(entry.modulePath);
    });
    objectResults.appendChild(row);
  });
  if (matches.length > 50) {
    const more = document.createElement("div");
    more.className = "muted";
    more.textContent = `Showing first 50 of ${matches.length} matches.`;
    objectResults.appendChild(more);
  }
}

function toggleModule(path) {
  if (!path || path === ROOT_KEY) {
    return;
  }
  if (state.expandedModules.has(path)) {
    state.expandedModules.delete(path);
    tweenZoomTo(DEFAULT_ZOOM);
  } else {
    state.expandedModules.add(path);
    tweenZoomTo(DEFAULT_ZOOM * 0.92);
  }
  updateGraph();
}

function captureViewState() {
  return {
    selectedId: state.selectedId,
    expandedModules: Array.from(state.expandedModules),
    camera: {
      x: camera.position.x,
      y: camera.position.y,
      zoom: camera.zoom,
    },
  };
}

function restoreViewState(snapshot) {
  if (!snapshot) {
    return;
  }
  state.historyLocked = true;
  state.expandedModules = new Set(snapshot.expandedModules || []);
  updateGraph();
  if (snapshot.selectedId) {
    setSelected(snapshot.selectedId);
  } else {
    setSelected(null);
  }
  if (snapshot.camera) {
    camera.position.set(snapshot.camera.x || 0, snapshot.camera.y || 0, 200);
    camera.zoom = snapshot.camera.zoom || DEFAULT_ZOOM;
    camera.updateProjectionMatrix();
  }
  state.historyLocked = false;
}

function pushHistory() {
  if (state.historyLocked) {
    return;
  }
  const snapshot = captureViewState();
  const last = state.history[state.history.length - 1];
  if (last && last.selectedId === snapshot.selectedId) {
    return;
  }
  state.history.push(snapshot);
  updateBackButton();
}

function popHistory() {
  if (state.history.length === 0) {
    return null;
  }
  const snapshot = state.history.pop();
  updateBackButton();
  return snapshot;
}

function updateBackButton() {
  if (!navBackButton) {
    return;
  }
  navBackButton.disabled = state.history.length === 0;
}

function expandModulePath(path) {
  if (!path || path === ROOT_KEY) {
    return;
  }
  let current = path;
  while (current && current !== ROOT_KEY) {
    state.expandedModules.add(current);
    const parent = state.graph.modules[current]?.parent || "";
    if (!parent || parent === current) {
      break;
    }
    current = parent;
  }
}

function focusModulePath(path) {
  if (!path) {
    return;
  }
  pushHistory();
  expandModulePath(path);
  updateGraph();
  focusModule(path);
}

function focusObjectBySignature(signature) {
  if (!signature) {
    return;
  }
  const entries = state.objectRefIndex.get(signature) || [];
  const modules = pickProviderModules(entries);
  if (modules.length === 0) {
    return;
  }
  focusModulePath(modules[0]);
}

function setSelected(id) {
  state.selectedId = id;
  applyHighlight();
  renderDetails();
}

function applyHighlight() {
  const selected = state.selectedId;
  const connected = new Set();

  if (selected) {
    state.edges.forEach(([from, to]) => {
      const fromId = `module:${from}`;
      const toId = `module:${to}`;
      if (fromId === selected || toId === selected) {
        connected.add(fromId);
        connected.add(toId);
      }
    });
  }

  state.nodes.forEach((node) => {
    if (!node.mesh.visible) {
      return;
    }
    if (selected && node.id === selected) {
      node.mesh.material.color.set(palette.highlight);
      node.mesh.material.opacity = 1;
    } else if (connected.has(node.id)) {
      node.mesh.material.color.set(palette.connected);
      node.mesh.material.opacity = 1;
    } else {
      node.mesh.material.color.set("white");
      node.mesh.material.opacity = 1;
    }
  });

  state.edgeMeshes.forEach((line) => {
    const { from, to } = line.userData;
    if (selected && (from === selected || to === selected)) {
      line.material.opacity = 0.9;
    } else {
      line.material.opacity = 0.25;
    }
  });
}

function renderDetails() {
  const selected = state.selectedId;
  const previousScroll = detailsPanel ? detailsPanel.scrollTop : 0;
  if (!selected) {
    detailsBody.innerHTML = '<div class="muted">Select a module to inspect dependencies.</div>';
    if (detailsPanel) {
      detailsPanel.scrollTop = previousScroll;
    }
    return;
  }
  const node = state.nodes.get(selected);
  if (!node) {
    detailsBody.innerHTML = '<div class="muted">Selection not found.</div>';
    if (detailsPanel) {
      detailsPanel.scrollTop = previousScroll;
    }
    return;
  }

  const modulePath = node.modulePath;
  if (modulePath === ROOT_KEY) {
    detailsBody.innerHTML = `
      <div class="details-section">
        <div class="label">Root</div>
        <div class="details-chip">All modules</div>
      </div>
    `;
    if (detailsPanel) {
      detailsPanel.scrollTop = previousScroll;
    }
    return;
  }
  const moduleInfo = state.graph.modules[modulePath] || {};
  const provided = collectProvidedObjects(modulePath);
  const depends = collectDependencyObjects(modulePath);
  const dependents = collectDependentModules(modulePath);

  detailsBody.innerHTML = `
    <div class="details-section">
      <div class="label">Module</div>
      <div class="details-chip">${escapeHtml(modulePath)}</div>
    </div>
    <div class="details-section">
      <div class="label">Description</div>
      <div class="details-chip">${escapeHtml(moduleInfo.description || "—")}</div>
    </div>
    <div class="details-section">
      <div class="label">Depends On Objects</div>
      <div class="details-list">
        ${renderObjectChips(depends)}
      </div>
    </div>
    <div class="details-section">
      <div class="label">Provides Objects</div>
      <div class="details-list">
        ${renderObjectChips(provided)}
      </div>
    </div>
    <div class="details-section">
      <div class="label">Dependent Modules</div>
      <div class="details-list">
        ${renderModuleChips(dependents)}
      </div>
    </div>
  `;
  if (detailsPanel) {
    detailsPanel.scrollTop = previousScroll;
  }
}

function collectProvidedObjects(modulePath) {
  const modules = collectModuleSubtree(modulePath);
  const items = [];
  Object.values(state.graph.objects || {}).forEach((obj) => {
    if (!modules.has(obj.modulePath)) {
      return;
    }
    if (!obj.providedBy || obj.providedBy.length === 0) {
      return;
    }
    items.push(objectInfoFromGraphObject(obj));
  });
  return uniqueSortedObjects(items);
}

function collectDependencyObjects(modulePath) {
  const modules = collectModuleSubtree(modulePath);
  const items = new Map();
  const addRefs = (refs) => {
    if (!refs) {
      return;
    }
    refs.forEach((ref) => {
      const info = objectInfoFromRef(ref);
      const key = info.signature || info.label;
      if (!items.has(key)) {
        info.optional = Boolean(info.optional);
        items.set(key, info);
        return;
      }
      const existing = items.get(key);
      const merged = new Set([...(existing.providers || []), ...(info.providers || [])]);
      existing.providers = Array.from(merged).sort();
      if (!existing.modulePath && existing.providers.length > 0) {
        existing.modulePath = existing.providers[0];
      }
      existing.optional = Boolean(existing.optional) && Boolean(info.optional);
    });
  };
  Object.values(state.graph.constructors || {}).forEach((ctor) => {
    if (!modules.has(ctor.modulePath)) {
      return;
    }
    addRefs(ctor.inputs);
  });
  Object.values(state.graph.invokers || {}).forEach((inv) => {
    if (!modules.has(inv.modulePath)) {
      return;
    }
    addRefs(inv.inputs);
  });
  Object.values(state.graph.decorators || {}).forEach((decor) => {
    if (!modules.has(decor.modulePath)) {
      return;
    }
    addRefs(decor.inputs);
  });
  return Array.from(items.values()).sort((a, b) => a.label.localeCompare(b.label));
}

function collectDependentModules(modulePath) {
  const modules = collectModuleSubtree(modulePath);
  const dependents = new Set();
  Object.values(state.graph.objects || {}).forEach((obj) => {
    if (!modules.has(obj.modulePath)) {
      return;
    }
    if (!obj.providedBy || obj.providedBy.length === 0) {
      return;
    }
    (obj.consumedBy || []).forEach((consumerId) => {
      const consumerModule = moduleForNodeId(consumerId);
      if (!consumerModule || consumerModule === ROOT_KEY) {
        return;
      }
      if (modules.has(consumerModule)) {
        return;
      }
      if (state.graph.modules && state.graph.modules[consumerModule]) {
        dependents.add(consumerModule);
      }
    });
  });
  return Array.from(dependents).sort();
}

function collectModuleSubtree(modulePath) {
  const modules = new Set();
  if (!modulePath) {
    return modules;
  }
  const walk = (path) => {
    modules.add(path);
    const children = (state.graph.modules[path]?.children || []);
    children.forEach((child) => walk(child));
  };
  walk(modulePath);
  return modules;
}

function formatObject(obj) {
  if (!obj) {
    return "";
  }
  let label = obj.type || "unknown";
  if (obj.name) {
    label += ` (name=${obj.name})`;
  }
  if (obj.group) {
    label += ` [group=${obj.group}]`;
  }
  return label;
}

function objectSignature(type, name, group) {
  return `${type || ""}|${name || ""}|${group || ""}`;
}

function providerModulesForObject(obj) {
  const providers = new Set();
  (obj.providedBy || []).forEach((providerId) => {
    let modulePath = "";
    if (state.graph.constructors && state.graph.constructors[providerId]) {
      modulePath = state.graph.constructors[providerId].modulePath || "";
    } else if (state.graph.decorators && state.graph.decorators[providerId]) {
      modulePath = state.graph.decorators[providerId].modulePath || "";
    }
    if (modulePath && state.graph.modules[modulePath]) {
      providers.add(modulePath);
    }
  });
  return Array.from(providers).sort();
}

function objectInfoFromGraphObject(obj) {
  const providers = providerModulesForObject(obj);
  return {
    label: formatObject(obj),
    signature: objectSignature(obj.type, obj.name, obj.group),
    providers,
    modulePath: providers[0] || obj.modulePath || "",
    isPrivate: isPrivateObject(obj),
    optional: false,
  };
}

function objectInfoFromRef(ref) {
  const signature = signatureForRef(ref);
  const entries = state.objectRefIndex.get(signature) || [];
  const providers = pickProviderModules(entries);
  const isPrivate =
    entries.length > 0 && entries.every((entry) => entry.isPrivate);
  return {
    label: formatObject(ref),
    signature,
    providers,
    modulePath: providers[0] || "",
    isPrivate,
    optional: Boolean(ref.optional),
  };
}

function pickProviderModules(entries) {
  const modules = new Set();
  entries.forEach((entry) => {
    (entry.providers || []).forEach((modulePath) => modules.add(modulePath));
  });
  return Array.from(modules).sort();
}

function signatureForRef(ref) {
  let type = ref.type || "";
  if (ref.group && type.startsWith("[]")) {
    type = type.slice(2);
  }
  return objectSignature(type, ref.name, ref.group);
}

function isPrivateObject(obj) {
  if (!obj) {
    return false;
  }
  const exportedValue =
    typeof obj.exported === "boolean"
      ? obj.exported
      : typeof obj.Exported === "boolean"
        ? obj.Exported
        : null;
  if (exportedValue !== null) {
    return !exportedValue;
  }
  if (!obj.providedBy || obj.providedBy.length === 0) {
    return false;
  }
  let hasCtor = false;
  let hasExported = false;
  obj.providedBy.forEach((providerId) => {
    const ctor = state.graph.constructors && state.graph.constructors[providerId];
    if (ctor) {
      hasCtor = true;
      if (ctor.exported || ctor.Exported) {
        hasExported = true;
      }
    }
  });
  return hasCtor && !hasExported;
}

function uniqueSortedObjects(list) {
  const map = new Map();
  list.filter(Boolean).forEach((info) => {
    const key = `${info.signature || info.label}|${info.modulePath || ""}`;
    map.set(key, info);
  });
  return Array.from(map.values()).sort((a, b) => a.label.localeCompare(b.label));
}

function renderObjectChips(items) {
  if (!items || items.length === 0) {
    return '<div class="muted">None</div>';
  }
  return items
    .map((item) => {
      const modulePath = item.modulePath || (item.providers || [])[0] || "";
      const privateClass = item.isPrivate ? " private" : "";
      const optionalClass = item.optional ? " optional" : "";
      const optionalSuffix = item.optional ? " (optional)" : "";
      if (!modulePath) {
        const title = item.optional
          ? "Optional dependency (no provider module found)"
          : "No provider module found";
        return `<div class="details-chip unresolved${privateClass}${optionalClass}" title="${escapeHtml(
          title
        )}">${escapeHtml(item.label)}${escapeHtml(optionalSuffix)}</div>`;
      }
      const signature = item.signature ? ` data-signature="${escapeHtml(item.signature)}"` : "";
      const title = item.optional ? "Optional dependency" : "";
      return `<button type="button" class="details-chip object-chip${privateClass}${optionalClass}" data-module="${escapeHtml(
        modulePath
      )}"${signature}${title ? ` title="${escapeHtml(title)}"` : ""}>${escapeHtml(
        item.label
      )}${escapeHtml(optionalSuffix)}</button>`;
    })
    .join("");
}

function renderModuleChips(modules) {
  if (!modules || modules.length === 0) {
    return '<div class="muted">None</div>';
  }
  return modules
    .map(
      (modulePath) =>
        `<button type="button" class="details-chip object-chip" data-module="${escapeHtml(
          modulePath
        )}">${escapeHtml(modulePath)}</button>`
    )
    .join("");
}

function escapeHtml(value) {
  return String(value || "")
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

function createModuleSprite(text) {
  const canvas = document.createElement("canvas");
  const context = canvas.getContext("2d");
  const fontSize = 16;
  const paddingX = 16;
  const paddingY = 10;
  context.font = `${fontSize}px "Space Grotesk", sans-serif`;
  const textWidth = context.measureText(text).width;
  const width = textWidth + paddingX * 2;
  const height = fontSize + paddingY * 2;
  canvas.width = Math.ceil(width);
  canvas.height = Math.ceil(height);
  context.font = `${fontSize}px "Space Grotesk", sans-serif`;
  context.fillStyle = "rgb(255, 250, 240)";
  context.strokeStyle = "rgb(31, 122, 140)";
  context.lineWidth = 2;
  drawRoundedRect(context, 2, 2, canvas.width - 4, canvas.height - 4, 12);
  context.fill();
  context.stroke();
  context.fillStyle = "#1b1b1b";
  context.textBaseline = "middle";
  context.fillText(text, paddingX, canvas.height / 2);

  const texture = new THREE.CanvasTexture(canvas);
  const material = new THREE.SpriteMaterial({ map: texture, transparent: true });
  const sprite = new THREE.Sprite(material);
  const scale = 0.22;
  sprite.scale.set(canvas.width * scale, canvas.height * scale, 1);
  return sprite;
}

function moduleDisplayName(path) {
  if (!path || path === ROOT_KEY) {
    return "root";
  }
  const parts = path.split(".");
  return parts[parts.length - 1] || path;
}

function createRootSprite() {
  const canvas = document.createElement("canvas");
  const context = canvas.getContext("2d");
  const size = 64;
  canvas.width = size;
  canvas.height = size;
  context.fillStyle = "rgba(31, 122, 140, 0.4)";
  context.strokeStyle = "rgba(31, 122, 140, 0.8)";
  context.lineWidth = 3;
  context.beginPath();
  context.arc(size / 2, size / 2, size / 2 - 6, 0, Math.PI * 2);
  context.fill();
  context.stroke();
  const texture = new THREE.CanvasTexture(canvas);
  const material = new THREE.SpriteMaterial({ map: texture, transparent: true });
  const sprite = new THREE.Sprite(material);
  sprite.scale.set(12, 12, 1);
  return sprite;
}
function drawRoundedRect(context, x, y, width, height, radius) {
  if (typeof context.roundRect === "function") {
    context.beginPath();
    context.roundRect(x, y, width, height, radius);
    return;
  }
  const r = Math.min(radius, width / 2, height / 2);
  context.beginPath();
  context.moveTo(x + r, y);
  context.lineTo(x + width - r, y);
  context.quadraticCurveTo(x + width, y, x + width, y + r);
  context.lineTo(x + width, y + height - r);
  context.quadraticCurveTo(x + width, y + height, x + width - r, y + height);
  context.lineTo(x + r, y + height);
  context.quadraticCurveTo(x, y + height, x, y + height - r);
  context.lineTo(x, y + r);
  context.quadraticCurveTo(x, y, x + r, y);
}

function updatePointer(event) {
  const rect = renderer.domElement.getBoundingClientRect();
  pointer.x = ((event.clientX - rect.left) / rect.width) * 2 - 1;
  pointer.y = -((event.clientY - rect.top) / rect.height) * 2 + 1;
}

function pick(event, select) {
  updatePointer(event);
  raycaster.setFromCamera(pointer, camera);
  const meshes = state.nodeMeshes.filter((mesh) => mesh.visible);
  const intersects = raycaster.intersectObjects(meshes, false);
  if (intersects.length > 0) {
    const mesh = intersects[0].object;
    const id = mesh.userData.id;
    if (select) {
      setSelected(id);
    }
    showTooltip(mesh.userData, event.clientX, event.clientY);
    return;
  }
  const edgeIntersects = raycaster.intersectObjects(state.edgeMeshes, false);
  if (edgeIntersects.length > 0) {
    const edge = edgeIntersects[0].object;
    const data = edge.userData || {};
    if (data.kind === "dep" && data.objects && data.objects.length > 0) {
      showEdgeTooltip(data, event.clientX, event.clientY);
      return;
    }
  }
  hideTooltip();
  if (select) {
    setSelected(null);
  }
}

function showTooltip(data, x, y) {
  tooltip.textContent = data.label;
  tooltip.style.left = `${x + 12}px`;
  tooltip.style.top = `${y + 12}px`;
  tooltip.hidden = false;
}

function showEdgeTooltip(data, x, y) {
  const objects = data.objects || [];
  const lines = objects.slice(0, EDGE_TOOLTIP_MAX);
  const extra = objects.length - lines.length;
  const header =
    data.fromModule && data.toModule
      ? `${data.fromModule} depends on ${data.toModule}`
      : "Dependency";
  const text = [
    header,
    "",
    "Objects:",
    ...lines.map((label) => `- ${label}`),
    extra > 0 ? `+${extra} more` : "",
  ]
    .filter(Boolean)
    .join("\n");
  tooltip.textContent = text;
  tooltip.style.left = `${x + 12}px`;
  tooltip.style.top = `${y + 12}px`;
  tooltip.hidden = false;
}

function hideTooltip() {
  tooltip.hidden = true;
}

function tweenZoom(multiplier) {
  const target = Math.min(2.5, Math.max(0.4, camera.zoom * multiplier));
  zoomTween = {
    from: camera.zoom,
    to: target,
    start: performance.now(),
    duration: 260,
  };
}

function focusModule(path) {
  const node = state.nodes.get(`module:${path}`);
  if (!node) {
    return;
  }
  tweenZoomTo(1.02);
  panTo(node.mesh.position);
  pulseNode(node);
  setSelected(node.id);
}

function tweenZoomTo(target) {
  const clamped = Math.min(2.5, Math.max(0.4, target));
  zoomTween = {
    from: camera.zoom,
    to: clamped,
    start: performance.now(),
    duration: 320,
  };
}

function panTo(position) {
  panTween = {
    from: { x: camera.position.x, y: camera.position.y },
    to: { x: position.x, y: position.y },
    start: performance.now(),
    duration: 320,
  };
}

function pulseNode(node) {
  pulseTargets.add(node);
  setTimeout(() => {
    pulseTargets.delete(node);
    node.mesh.scale.set(node.baseScale.x, node.baseScale.y, 1);
  }, 1200);
}

detailsBody.addEventListener("click", (event) => {
  const target = event.target.closest(".object-chip");
  if (!target) {
    return;
  }
  const modulePath = target.dataset.module || "";
  const signature = target.dataset.signature || "";
  if (modulePath) {
    focusModulePath(modulePath);
    return;
  }
  focusObjectBySignature(signature);
});

viewport.addEventListener("pointerdown", (event) => {
  isDragging = true;
  lastPointer = { x: event.clientX, y: event.clientY };
});

viewport.addEventListener("pointerup", (event) => {
  if (isDragging) {
    const moved = Math.hypot(
      event.clientX - lastPointer.x,
      event.clientY - lastPointer.y
    );
    if (moved < 4) {
      pick(event, true);
    }
  }
  isDragging = false;
});

window.addEventListener("pointerup", () => {
  isDragging = false;
  lastPointer = null;
});

viewport.addEventListener("pointermove", (event) => {
  pick(event, false);
  if (!isDragging || !lastPointer) {
    return;
  }
  const dx = event.clientX - lastPointer.x;
  const dy = event.clientY - lastPointer.y;
  camera.position.x -= dx / camera.zoom;
  camera.position.y += dy / camera.zoom;
  lastPointer = { x: event.clientX, y: event.clientY };
});

viewport.addEventListener(
  "wheel",
  (event) => {
    event.preventDefault();
    const delta = Math.sign(event.deltaY) * -0.1;
    camera.zoom = Math.min(2.5, Math.max(0.4, camera.zoom + delta));
    camera.updateProjectionMatrix();
  },
  { passive: false }
);

viewport.addEventListener("pointerleave", hideTooltip);

viewport.addEventListener("dblclick", (event) => {
  updatePointer(event);
  raycaster.setFromCamera(pointer, camera);
  const meshes = state.nodeMeshes.filter((mesh) => mesh.visible);
  const intersects = raycaster.intersectObjects(meshes, false);
  if (intersects.length === 0) {
    return;
  }
  const mesh = intersects[0].object;
  const nodeId = mesh.userData.id;
  const node = state.nodes.get(nodeId);
  if (node) {
    pushHistory();
    toggleModule(node.modulePath);
    focusModule(node.modulePath);
  }
});

document.getElementById("reset-view").addEventListener("click", () => {
  camera.position.set(0, 0, 200);
  camera.zoom = DEFAULT_ZOOM;
  camera.updateProjectionMatrix();
});

if (navBackButton) {
  navBackButton.addEventListener("click", () => {
    const snapshot = popHistory();
    if (snapshot) {
      restoreViewState(snapshot);
    }
  });
}

document.getElementById("expand-all").addEventListener("click", () => {
  state.moduleOrder.forEach((path) => state.expandedModules.add(path));
  tweenZoom(1.05);
  updateGraph();
});

document.getElementById("collapse-all").addEventListener("click", () => {
  state.expandedModules.clear();
  tweenZoom(0.95);
  updateGraph();
});

function bootstrap() {
  fetchGraph()
    .then((graph) => {
      state.graph = graph;
      buildModuleDeps();
      buildNodes();
      buildObjectIndex();
      updateGraph();
      setupObjectSearch();
      animate();
    })
    .catch((err) => {
      detailsBody.innerHTML = `<div class="muted">Failed to load graph: ${err}</div>`;
    });
}

bootstrap();
