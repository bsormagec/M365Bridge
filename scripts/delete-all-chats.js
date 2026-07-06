// M365 Copilot Chat Deletion Script
// Run in browser console at https://m365.cloud.microsoft/chat
// or via Playwright browser_evaluate.
//
// Deletes ALL chats in the sidebar. Preserves agent chats
// ("Study and Learn", "Microsoft 365 Admin").
//
// REST API (/chat/api/RefreshNavPane) returns 500 — UI automation is the only way.
//
// Usage via Playwright MCP:
//   browser_navigate -> https://m365.cloud.microsoft/chat
//   browser_evaluate -> (paste this entire file as the function body)
//
// Returns: {"deleted": N, "skipped": N}

async function deleteAllChats() {
  const sleep = ms => new Promise(r => setTimeout(r, ms));
  await sleep(3000); // wait for sidebar to load
  let deleted = 0;
  let skipped = 0;

  for (let i = 0; i < 200; i++) {
    // Find all "More" buttons, filter out agent chats
    const moreBtns = Array.from(document.querySelectorAll('button[aria-label="More"]'));
    const chatBtns = moreBtns.filter(b => {
      const c = b.parentElement?.textContent || '';
      return !c.includes('Study and Learn') && !c.includes('Microsoft 365 Admin');
    });

    if (chatBtns.length === 0) break;

    // Click first chat's "More" button
    const btn = chatBtns[0];
    btn.scrollIntoView();
    await sleep(300);
    btn.click();
    await sleep(2000); // wait for context menu to open

    // Click "Delete" menu item
    const deleteItem = Array.from(document.querySelectorAll('[role="menuitem"]'))
      .find(m => m.textContent?.trim() === 'Delete');
    if (!deleteItem) {
      // Menu didn't open or no Delete option — dismiss and skip
      document.body.click();
      await sleep(1000);
      skipped++;
      continue;
    }
    deleteItem.click();
    await sleep(2500); // wait for confirm dialog to open

    // Click "Delete" in the confirm dialog ([role="alertdialog"])
    const dialog = document.querySelector('[role="alertdialog"]');
    if (dialog) {
      const delBtn = Array.from(dialog.querySelectorAll('button'))
        .find(b => b.textContent?.trim() === 'Delete');
      if (delBtn) {
        delBtn.click();
        await sleep(3000); // wait for deletion + sidebar refresh
        deleted++;
      } else {
        document.body.click();
        await sleep(1000);
        skipped++;
      }
    } else {
      skipped++;
    }
  }

  return JSON.stringify({ deleted, skipped }, null, 2);
}

// When run via Playwright browser_evaluate, the function is called directly.
// When run in browser console, call it manually:
// deleteAllChats().then(r => console.log(r));
deleteAllChats().then(r => { window.__deleteResult = r; });
