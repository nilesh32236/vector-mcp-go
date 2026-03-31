def normalize(text: str) -> str:
    """
    Normalize a string by removing surrounding whitespace and converting all characters to lowercase.
    
    Parameters:
        text (str): Input string to normalize.
    
    Returns:
        str: The input string with leading and trailing whitespace removed and all characters converted to lowercase.
    """
    return text.strip().lower()


def run(items: list[str]) -> list[str]:
    """
    Normalize each string in `items` by trimming whitespace and converting to lowercase, preserving the input order.
    
    Parameters:
        items (list[str]): List of strings to normalize; order is preserved.
    
    Returns:
        list[str]: A new list with each input string trimmed and lowercased in the same order as `items`.
    """
    return [normalize(i) for i in items]
