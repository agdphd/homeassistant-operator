console.info("Example Card fixture loaded");

class ExampleCard extends HTMLElement {
  setConfig(config) {
    this._config = config;
  }
  set hass(hass) {
    this.innerHTML = "<ha-card>Example Card fixture</ha-card>";
  }
}
customElements.define("example-card", ExampleCard);
