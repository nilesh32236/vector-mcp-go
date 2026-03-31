def normalize(text: str) -> str:
    return text.strip().lower()


def run(items: list[str]) -> list[str]:
    return [normalize(i) for i in items]
