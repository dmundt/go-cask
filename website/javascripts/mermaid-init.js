document$.subscribe(() => {
  mermaid.initialize({
    startOnLoad: true,
    securityLevel: "loose",
    theme: "base",
    flowchart: {
      curve: "linear",
      nodeSpacing: 40,
      rankSpacing: 50
    },
    themeVariables: {
      background: "#ffffff",
      primaryColor: "#ffffff",
      primaryTextColor: "#0f172a",
      primaryBorderColor: "#cbd5e1",
      lineColor: "#475569",
      secondaryColor: "#f8fafc",
      tertiaryColor: "#f8fafc",
      fontFamily: "\"Segoe UI\", system-ui, sans-serif"
    }
  });
});
