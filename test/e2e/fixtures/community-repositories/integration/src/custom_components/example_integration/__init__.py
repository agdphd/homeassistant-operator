"""Minimal HACS-compatible fixture integration used by community-repository E2E tests."""

DOMAIN = "example_integration"


async def async_setup(hass, config):
    return True
