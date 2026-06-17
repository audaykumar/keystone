(function () {
  const diagrams = document.querySelectorAll('[data-lab-diagram]');

  diagrams.forEach((diagram) => {
    const detail = document.getElementById(diagram.dataset.detailTarget);
    if (!detail) return;

    const kind = detail.querySelector('[data-detail-kind]');
    const title = detail.querySelector('[data-detail-title]');
    const body = detail.querySelector('[data-detail-body]');
    const nodes = Array.from(diagram.querySelectorAll('[data-kind][data-title][data-body]'));

    function select(node) {
      nodes.forEach((item) => item.classList.toggle('is-active', item === node));
      kind.textContent = node.dataset.kind;
      title.textContent = node.dataset.title;
      body.textContent = node.dataset.body;
    }

    nodes.forEach((node) => {
      node.addEventListener('click', () => select(node));
      node.addEventListener('keydown', (event) => {
        if (event.key === 'Enter' || event.key === ' ') {
          event.preventDefault();
          select(node);
        }
      });
    });

    if (nodes[0]) select(nodes[0]);
  });
})();
