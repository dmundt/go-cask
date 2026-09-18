document$.subscribe(() => {
  mermaid.initialize({
    startOnLoad: true,
    securityLevel: 'loose'
  });
});
