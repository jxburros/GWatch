// A network of about `size` nodes built the way a real one is — a gateway, a
// core switch, access switches, access points, hypervisors with their guests,
// and a few services that hang off nothing — for testing the network map at
// the size people actually run (one install it is tried against has 250+).
// Deterministic: the same size always gives the same network.

export function bigNetwork(size = 250, seed = 250) {
  let a = seed >>> 0;
  const r = () => { a = (a + 0x6D2B79F5) >>> 0; let t = a; t = Math.imul(t ^ (t >>> 15), t | 1); t ^= t + Math.imul(t ^ (t >>> 7), t | 61); return ((t ^ (t >>> 14)) >>> 0) / 4294967296; };
  const nodes = [];
  let id = 0;
  const add = (name, parent = null, extra = {}) => { const n = { id: ++id, name, host: `10.0.0.${id}`, dependsOnNodeId: parent, groups: [], tags: [], ...extra }; nodes.push(n); return n; };
  const gw = add('Gateway');
  const core = add('Core switch', gw.id);
  add('Pi-hole', gw.id);
  const rooms = ['Office', 'Lounge', 'Kitchen', 'Garage', 'Loft', 'Studio', 'Workshop', 'Basement', 'Annex'];
  const access = rooms.map((room) => add(`${room} switch`, core.id));
  const hyper = [add('Hypervisor 1', core.id), add('Hypervisor 2', core.id)];
  for (let i = 0; i < 5; i++) add(`Service ${String.fromCharCode(65 + i)} website`);
  const aps = [];
  const kinds = ['Desk PC', 'Laptop', 'Phone', 'TV', 'Speaker', 'Camera', 'Thermostat', 'Printer', 'Light hub', 'Tablet'];
  let k = 0;
  while (nodes.length < size) {
    const roll = r();
    if (roll < 0.06 && aps.length < 12) aps.push(add(`AP ${aps.length + 1}`, access[aps.length % access.length].id));
    else if (roll < 0.2) add(`VM ${String(++k).padStart(3, '0')}`, hyper[k % 2].id);
    else {
      const list = aps.length && r() < 0.45 ? aps : access;
      add(`${kinds[Math.floor(r() * kinds.length)]} ${String(++k).padStart(3, '0')}`, list[Math.floor(r() * list.length)].id);
    }
  }
  return nodes;
}
